package uiapp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type concealedClipboardWriter interface {
	WriteConcealedClipboard(text string) (int64, error)
}

// ClipboardCopyOrigin identifies who initiated a clipboard copy or paste action.
// Sensitive copies are strictly restricted to ClipboardCopyOriginUser.
type ClipboardCopyOrigin string

const (
	ClipboardCopyOriginUser      ClipboardCopyOrigin = "user"
	ClipboardCopyOriginAgentTool ClipboardCopyOrigin = "agent_tool"
)

// ErrSensitiveCopyUserOnly is returned whenever a non-user caller (such as an
// Agent tool) attempts to copy sensitive or password-like content.
var ErrSensitiveCopyUserOnly = errors.New("sensitive clipboard copy is user-only and forbidden for agent tools")

// ErrClipboardActionUnavailable is returned when an action (such as Copy Path)
// has no applicable payload on the chosen entry.
var ErrClipboardActionUnavailable = errors.New("clipboard action unavailable for this entry")

// ClipboardCopyRequest describes a user-initiated clipboard capability call.
type ClipboardCopyRequest struct {
	Origin       ClipboardCopyOrigin
	Action       config.ClipboardActionKind
	Item         clipboard.ClipboardItem
	Sensitive    bool
	PasswordLike bool
	// SecretLabel is a non-secret display caption (e.g. "API token" or
	// "Keychain entry") used in HUD/TTL banners; raw secret text is never shown.
	SecretLabel string
	// TTL overrides the policy default for sensitive copies when > 0.
	// Values <= 0 fall back to the configured policy TTL (default 30s).
	TTL time.Duration
}

// ClipboardActionResult summarizes a completed clipboard capability without
// ever leaking sensitive payload text into logs or UI strings.
type ClipboardActionResult struct {
	Action             config.ClipboardActionKind
	Copied             bool
	Pasted             bool
	Sensitive          bool
	ExcludedFromStore  bool
	AutoClearTTL       time.Duration
	AutoClearExpiresAt time.Time
	Summary            string
}

// SensitiveClipboardStatus reports the current auto-clear countdown state
// without exposing the copied secret value.
type SensitiveClipboardStatus struct {
	Active    bool
	Label     string
	TTL       time.Duration
	ExpiresAt time.Time
	Remaining time.Duration
}

type clipboardKeyManagerState struct {
	mu                  sync.Mutex
	policy              config.ClipboardKeyManagerPolicy
	sensitiveActive     bool
	sensitiveLabel      string
	sensitiveHash       string
	sensitiveSeq        int64
	sensitiveHasSeq     bool
	sensitiveTTL        time.Duration
	sensitiveExpiresAt  time.Time
	sensitiveGen        uint64
	sensitiveTimer      *time.Timer
	sensitiveVaultToken string
	sensitiveVaultClean func() error
	vaultClipboard      *hostVaultClipboard
	recentSecretHashes  map[string]time.Time
	clearHook           func() error
	readHook            func() (string, bool)
	lastStatus          string
	lastError           string
	demoItem            *clipboard.ClipboardItem
	demoSecretValue     string
	demoSecretLabel     string
}

func (a *AppController) clipboardKeyManager() *clipboardKeyManagerState {
	return a.clipboardKeyManagerStateFor()
}

// ClipboardKeyManagerPolicy returns the active clipboard key-manager policy.
func (a *AppController) ClipboardKeyManagerPolicy() config.ClipboardKeyManagerPolicy {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	defer st.mu.Unlock()
	out := st.policy
	out.Bindings = append([]config.ClipboardActionBinding(nil), st.policy.Bindings...)
	return out
}

// SetClipboardSensitiveTTLSeconds updates the auto-clear TTL (clamped to
// 5..300s) for user-only sensitive clipboard copies.
func (a *AppController) SetClipboardSensitiveTTLSeconds(sec int) int {
	norm := config.NormalizeSensitiveTTLSeconds(sec)
	st := a.clipboardKeyManager()
	st.mu.Lock()
	st.policy.SensitiveTTLSeconds = norm
	st.mu.Unlock()
	return norm
}

// SetSensitiveClipboardClearHook overrides the clear callback invoked when a
// sensitive copy TTL expires (defaults to writing "" via writeClipboardText).
func (a *AppController) SetSensitiveClipboardClearHook(fn func() error) {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	st.clearHook = fn
	st.mu.Unlock()
}

// SetSensitiveClipboardReadHook overrides clipboard text inspection used to
// verify clipboard ownership before clearing on sensitive TTL expiry.
func (a *AppController) SetSensitiveClipboardReadHook(fn func() (string, bool)) {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	st.readHook = fn
	st.mu.Unlock()
}

func (a *AppController) clipboardSequence() (int64, bool) {
	if a == nil {
		return 0, false
	}
	a.mu.Lock()
	br := a.Bridge
	a.mu.Unlock()
	seq, ok := br.(bridge.ClipboardSequencer)
	if !ok || seq == nil {
		return 0, false
	}
	return seq.ClipboardSequence(), true
}

func (a *AppController) readClipboardTextForOwnership() (string, bool) {
	if a == nil {
		return "", false
	}
	st := a.clipboardKeyManager()
	st.mu.Lock()
	readHook := st.readHook
	st.mu.Unlock()
	if readHook != nil {
		return readHook()
	}
	if mygo.App.IsReady() {
		return mygo.Clipboard.ReadText(), true
	}
	return "", false
}

// clipboardStillOwned verifies that the clipboard has not been replaced by
// another copy since the sensitive write. Both pasteboard change sequence
// (when available) and content hash (when readable) must still match.
func (a *AppController) clipboardStillOwned(expectedHash string, expectedSeq int64, hasSeq bool) bool {
	if hasSeq {
		if curSeq, ok := a.clipboardSequence(); ok && curSeq != expectedSeq {
			return false
		}
	}
	if expectedHash != "" {
		if curText, ok := a.readClipboardTextForOwnership(); ok {
			if hashClipboardPayload(curText) != expectedHash {
				return false
			}
		}
	}
	return true
}

func (a *AppController) writeSensitiveClipboard(value string) (int64, bool, error) {
	if a == nil {
		return 0, false, errors.New("controller unavailable")
	}
	a.mu.Lock()
	br := a.Bridge
	a.mu.Unlock()

	var seq bridge.ClipboardSequencer
	if s, ok := br.(bridge.ClipboardSequencer); ok && s != nil {
		seq = s
		if br != nil {
			prev := seq.ClipboardSequence()
			br.IgnoreSelfPasteboardChange(prev + 1)
		}
	}

	if cw, ok := br.(concealedClipboardWriter); ok && cw != nil {
		if writtenSeq, err := cw.WriteConcealedClipboard(value); err == nil {
			if br != nil {
				br.IgnoreSelfPasteboardChange(writtenSeq)
			}
			return writtenSeq, true, nil
		}
	}

	if err := a.writeClipboardText(value); err != nil {
		return 0, false, err
	}

	if seq != nil {
		post := seq.ClipboardSequence()
		if br != nil {
			br.IgnoreSelfPasteboardChange(post)
		}
		return post, true, nil
	}
	return 0, false, nil
}

func (a *AppController) clearClipboardTextSafe() error {
	if a == nil {
		return errors.New("controller unavailable")
	}
	a.mu.Lock()
	br := a.Bridge
	a.mu.Unlock()

	var seq bridge.ClipboardSequencer
	if s, ok := br.(bridge.ClipboardSequencer); ok && s != nil {
		seq = s
		if br != nil {
			prev := seq.ClipboardSequence()
			br.IgnoreSelfPasteboardChange(prev + 1)
		}
	}
	err := a.writeClipboardText("")
	if err == nil && seq != nil && br != nil {
		br.IgnoreSelfPasteboardChange(seq.ClipboardSequence())
	}
	return err
}

// SetClipboardKeyManagerDemoTarget seeds a non-secret sample item and optional
// sensitive test payload for the interactive Clipboard Key Manager workbench.
func (a *AppController) SetClipboardKeyManagerDemoTarget(item clipboard.ClipboardItem, secretLabel, secretValue string) {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	cp := item
	st.demoItem = &cp
	st.demoSecretLabel = strings.TrimSpace(secretLabel)
	st.demoSecretValue = secretValue
	st.mu.Unlock()
}

// SensitiveClipboardState returns the active sensitive copy countdown (never
// containing any raw secret value).
func (a *AppController) SensitiveClipboardState() SensitiveClipboardStatus {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.sensitiveActive {
		return SensitiveClipboardStatus{}
	}
	rem := time.Until(st.sensitiveExpiresAt)
	if rem < 0 {
		rem = 0
	}
	return SensitiveClipboardStatus{
		Active:    true,
		Label:     st.sensitiveLabel,
		TTL:       st.sensitiveTTL,
		ExpiresAt: st.sensitiveExpiresAt,
		Remaining: rem,
	}
}

func hashClipboardPayload(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// IsPasswordOrSecretContent detects password-like or credential payloads that
// must never enter normal clipboard history.
func IsPasswordOrSecretContent(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	prefixes := []string{
		"sk-", "sk_live_", "sk_test_", "ghp_", "gho_", "ghu_", "ghs_", "github_pat_",
		"akia", "asia", "xoxb-", "xoxp-", "bearer ", "-----begin ",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	markers := []string{
		"-----begin private key-----",
		"-----begin rsa private key-----",
		"-----begin openssh private key-----",
		"password=", "passwd=", "api_key=", "apikey=", "secret_key=", "access_token=",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// IsPasswordOrSecretCandidate reports whether a clipboard capture candidate is
// a password-manager, concealed, or secret-shaped entry that must be excluded
// from normal clipboard history.
func IsPasswordOrSecretCandidate(cfg config.ClipboardConfig, candidate clipboard.CaptureCandidate) bool {
	filter := clipboard.NewPrivacyFilterFromConfig(cfg)
	if !filter.ShouldRecord(candidate) {
		return true
	}
	return IsPasswordOrSecretContent(candidate.Content)
}

// ShouldSkipClipboardHistory reports true if candidate must NOT be recorded
// into normal clipboard history — either because it is password/concealed/secret
// shaped OR because its SHA-256 matches an active user-only sensitive copy.
func (a *AppController) ShouldSkipClipboardHistory(candidate clipboard.CaptureCandidate) bool {
	if IsPasswordOrSecretCandidate(a.Config().Main.Clipboard, candidate) {
		return true
	}
	h := hashClipboardPayload(candidate.Content)
	now := time.Now()
	st := a.clipboardKeyManager()
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, exp := range st.recentSecretHashes {
		if now.After(exp) {
			delete(st.recentSecretHashes, k)
		}
	}
	if exp, ok := st.recentSecretHashes[h]; ok && now.Before(exp) {
		return true
	}
	return false
}

// FormatClipboardActionPayload computes the text payload for Copy Content,
// Copy ID, Copy Path, or secret-safe Copy Metadata.
func FormatClipboardActionPayload(action config.ClipboardActionKind, item clipboard.ClipboardItem) (string, error) {
	switch action {
	case config.ClipboardActionCopyContent, config.ClipboardActionCopySensitive:
		if item.IsMedia() && strings.TrimSpace(item.Content) == "" {
			if strings.TrimSpace(item.MediaPath) != "" {
				return item.MediaPath, nil
			}
			return "", ErrClipboardActionUnavailable
		}
		if item.Content == "" {
			return "", ErrClipboardActionUnavailable
		}
		return item.Content, nil

	case config.ClipboardActionCopyID:
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return "", ErrClipboardActionUnavailable
		}
		return id, nil

	case config.ClipboardActionCopyPath:
		paths := clipboard.SplitFilePaths(item.MediaPath)
		if len(paths) > 0 {
			return strings.Join(paths, "\n"), nil
		}
		if p := strings.TrimSpace(item.MediaPath); p != "" {
			return p, nil
		}
		return "", ErrClipboardActionUnavailable

	case config.ClipboardActionCopyMetadata:
		kind := item.Kind
		if kind == "" {
			kind = clipboard.KindText
		}
		lines := []string{
			fmt.Sprintf("id: %s", strings.TrimSpace(item.ID)),
			fmt.Sprintf("kind: %s", kind),
		}
		if app := strings.TrimSpace(item.SourceAppName); app != "" {
			lines = append(lines, fmt.Sprintf("source_app: %s", app))
		}
		if bid := strings.TrimSpace(item.SourceBundleID); bid != "" {
			lines = append(lines, fmt.Sprintf("source_bundle: %s", bid))
		}
		if !item.CreatedAt.IsZero() {
			lines = append(lines, fmt.Sprintf("created_at: %s", item.CreatedAt.UTC().Format(time.RFC3339)))
		}
		if item.ByteSize > 0 {
			lines = append(lines, fmt.Sprintf("bytes: %d", item.ByteSize))
		} else if item.Content != "" && !IsPasswordOrSecretContent(item.Content) {
			lines = append(lines, fmt.Sprintf("bytes: %d", len(item.Content)))
		}
		if item.Width > 0 && item.Height > 0 {
			lines = append(lines, fmt.Sprintf("dimensions: %dx%d", item.Width, item.Height))
		}
		if item.MediaPath != "" {
			lines = append(lines, fmt.Sprintf("path: %s", item.MediaPath))
		}
		lines = append(lines, fmt.Sprintf("pinned: %t", item.IsPinned))
		return strings.Join(lines, "\n"), nil

	default:
		return "", ErrClipboardActionUnavailable
	}
}

// ExecuteClipboardAction runs a clipboard copy or paste capability with strict
// user-only enforcement for sensitive/password entries, history exclusion, and
// automatic TTL clipboard clearing.
func (a *AppController) ExecuteClipboardAction(req ClipboardCopyRequest) (ClipboardActionResult, error) {
	isSensitive := req.Sensitive ||
		req.PasswordLike ||
		req.Action == config.ClipboardActionCopySensitive ||
		IsPasswordOrSecretContent(req.Item.Content)

	// Hard security boundary: never allow Agent tools (or unspecified callers)
	// to invoke sensitive/password copy or paste.
	if isSensitive && req.Origin != ClipboardCopyOriginUser {
		a.setClipboardActionStatus("", ErrSensitiveCopyUserOnly.Error())
		return ClipboardActionResult{}, ErrSensitiveCopyUserOnly
	}
	if req.Origin == ClipboardCopyOriginAgentTool && req.Action == config.ClipboardActionPaste {
		a.setClipboardActionStatus("", ErrSensitiveCopyUserOnly.Error())
		return ClipboardActionResult{}, ErrSensitiveCopyUserOnly
	}

	if req.Action == config.ClipboardActionPaste {
		if req.Item.IsMedia() {
			a.pasteClipboardMediaAsync(engineClipboardMedia(req.Item.Media), nil)
		} else {
			if req.Item.Content == "" {
				return ClipboardActionResult{}, ErrClipboardActionUnavailable
			}
			a.pasteClipboardTextAsync(req.Item.Content, nil)
		}
		msg := pickLang(i18n.IsChinese(), "已触发粘贴动作", "Paste triggered")
		a.setClipboardActionStatus(msg, "")
		return ClipboardActionResult{
			Action:  req.Action,
			Pasted:  true,
			Summary: msg,
		}, nil
	}

	// Copy media directly for CopyContent on non-sensitive media items.
	if req.Action == config.ClipboardActionCopyContent && !isSensitive && req.Item.IsMedia() {
		if err := writeClipboardMedia(engineClipboardMedia(req.Item.Media)); err != nil {
			a.setClipboardActionStatus("", err.Error())
			return ClipboardActionResult{}, err
		}
		msg := pickLang(i18n.IsChinese(), "已复制媒体内容", "Copied media content")
		a.setClipboardActionStatus(msg, "")
		return ClipboardActionResult{Action: req.Action, Copied: true, Summary: msg}, nil
	}

	payload, err := FormatClipboardActionPayload(req.Action, req.Item)
	if err != nil {
		a.setClipboardActionStatus("", err.Error())
		return ClipboardActionResult{}, err
	}

	if isSensitive {
		return a.copySensitiveUserOnly(req, payload)
	}

	if err := a.writeClipboardText(payload); err != nil {
		a.setClipboardActionStatus("", err.Error())
		return ClipboardActionResult{}, err
	}

	// A normal non-sensitive copy cancels any pending sensitive auto-clear so
	// we don't accidentally wipe the user's newer normal clipboard entry.
	a.disarmSensitiveTimerOnly()

	msg := clipboardActionSuccessLabel(req.Action)
	a.setClipboardActionStatus(msg, "")
	return ClipboardActionResult{
		Action:  req.Action,
		Copied:  true,
		Summary: msg,
	}, nil
}

func (a *AppController) copySensitiveUserOnly(req ClipboardCopyRequest, payload string) (ClipboardActionResult, error) {
	if req.Origin != ClipboardCopyOriginUser {
		return ClipboardActionResult{}, ErrSensitiveCopyUserOnly
	}

	ttl := req.TTL
	if ttl <= 0 {
		policy := a.ClipboardKeyManagerPolicy()
		ttl = time.Duration(config.NormalizeSensitiveTTLSeconds(policy.SensitiveTTLSeconds)) * time.Second
	}
	if ttl < 50*time.Millisecond {
		ttl = 50 * time.Millisecond
	}

	payloadHash := hashClipboardPayload(payload)
	now := time.Now()
	expiresAt := now.Add(ttl)

	// Record exclusion hash BEFORE writing to the system clipboard so a fast
	// clipboard poller can never ingest this sensitive payload into history.
	st := a.clipboardKeyManager()
	st.mu.Lock()
	if st.recentSecretHashes == nil {
		st.recentSecretHashes = make(map[string]time.Time)
	}
	st.recentSecretHashes[payloadHash] = expiresAt.Add(15 * time.Second)
	vc := st.vaultClipboard
	st.mu.Unlock()

	if vc != nil {
		vc.invalidate()
	}

	expectedSeq, hasSeq, err := a.writeSensitiveClipboard(payload)
	if err != nil {
		a.setClipboardActionStatus("", err.Error())
		return ClipboardActionResult{}, err
	}

	label := strings.TrimSpace(req.SecretLabel)
	if label == "" {
		label = pickLang(i18n.IsChinese(), "敏感条目", "Sensitive item")
	}

	st.mu.Lock()
	if st.sensitiveTimer != nil {
		st.sensitiveTimer.Stop()
	}
	st.sensitiveGen++
	gen := st.sensitiveGen
	st.sensitiveActive = true
	st.sensitiveLabel = label
	st.sensitiveHash = payloadHash
	st.sensitiveSeq = expectedSeq
	st.sensitiveHasSeq = hasSeq
	st.sensitiveTTL = ttl
	st.sensitiveExpiresAt = expiresAt
	st.sensitiveVaultToken = ""
	st.sensitiveVaultClean = nil
	st.sensitiveTimer = time.AfterFunc(ttl, func() {
		a.clearSensitiveClipboardForGen(gen)
	})
	st.mu.Unlock()

	secDisplay := int(ttl.Seconds())
	if secDisplay <= 0 {
		secDisplay = 1
	}
	msg := pickLang(i18n.IsChinese(),
		fmt.Sprintf("已安全复制（%ds 后自动清除，不进入普通历史）", secDisplay),
		fmt.Sprintf("Copied sensitive value (auto-clears in %ds, excluded from history)", secDisplay))
	a.setClipboardActionStatus(msg, "")
	a.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: msg})
	return ClipboardActionResult{
		Action:             req.Action,
		Copied:             true,
		Sensitive:          true,
		ExcludedFromStore:  true,
		AutoClearTTL:       ttl,
		AutoClearExpiresAt: expiresAt,
		Summary:            msg,
	}, nil
}

func (a *AppController) disarmSensitiveTimerOnly() {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	if st.sensitiveTimer != nil {
		st.sensitiveTimer.Stop()
		st.sensitiveTimer = nil
	}
	st.sensitiveGen++
	st.sensitiveActive = false
	st.sensitiveHash = ""
	st.sensitiveSeq = 0
	st.sensitiveHasSeq = false
	st.sensitiveLabel = ""
	st.sensitiveExpiresAt = time.Time{}
	st.sensitiveVaultToken = ""
	st.sensitiveVaultClean = nil
	vc := st.vaultClipboard
	st.mu.Unlock()
	if vc != nil {
		vc.invalidate()
	}
}

func (a *AppController) clearSensitiveClipboardForGen(gen uint64) {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	if !st.sensitiveActive || st.sensitiveGen != gen {
		st.mu.Unlock()
		return
	}
	expectedHash := st.sensitiveHash
	expectedSeq := st.sensitiveSeq
	hasSeq := st.sensitiveHasSeq
	st.sensitiveActive = false
	st.sensitiveHash = ""
	st.sensitiveSeq = 0
	st.sensitiveHasSeq = false
	st.sensitiveLabel = ""
	st.sensitiveExpiresAt = time.Time{}
	st.sensitiveTimer = nil
	hook := st.clearHook
	st.mu.Unlock()

	if !a.clipboardStillOwned(expectedHash, expectedSeq, hasSeq) {
		skippedMsg := pickLang(i18n.IsChinese(), "剪贴板已被其他内容替换，已跳过自动清除", "Clipboard changed; skipped auto-clear")
		a.setClipboardActionStatus(skippedMsg, "")
		a.notifyClipboardKeyManagerRepaint()
		return
	}

	if hook != nil {
		_ = hook()
	} else {
		_ = a.clearClipboardTextSafe()
	}
	clearedMsg := pickLang(i18n.IsChinese(), "已自动清除敏感剪贴板内容", "Auto-cleared sensitive clipboard content")
	a.setClipboardActionStatus(clearedMsg, "")
	a.notifyClipboardKeyManagerRepaint()
}

// ClearSensitiveClipboardNow immediately wipes the system clipboard and
// disarms the active sensitive copy TTL timer.
func (a *AppController) ClearSensitiveClipboardNow() error {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	if st.sensitiveTimer != nil {
		st.sensitiveTimer.Stop()
		st.sensitiveTimer = nil
	}
	st.sensitiveGen++
	st.sensitiveActive = false
	st.sensitiveHash = ""
	st.sensitiveSeq = 0
	st.sensitiveHasSeq = false
	st.sensitiveLabel = ""
	st.sensitiveExpiresAt = time.Time{}
	st.sensitiveVaultToken = ""
	vaultClean := st.sensitiveVaultClean
	st.sensitiveVaultClean = nil
	hook := st.clearHook
	st.mu.Unlock()

	var err error
	if vaultClean != nil {
		err = vaultClean()
		if errors.Is(err, secrets.ErrOwnershipLost) {
			skippedMsg := pickLang(i18n.IsChinese(), "剪贴板已被其他内容替换，已跳过清除", "Clipboard changed; skipped clearing")
			a.setClipboardActionStatus(skippedMsg, "")
			a.notifyClipboardKeyManagerRepaint()
			return nil
		}
	} else if hook != nil {
		err = hook()
	} else {
		err = a.clearClipboardTextSafe()
	}
	if err != nil {
		a.setClipboardActionStatus("", err.Error())
		return err
	}
	msg := pickLang(i18n.IsChinese(), "已立即清除剪贴板敏感内容", "Cleared sensitive clipboard content")
	a.setClipboardActionStatus(msg, "")
	a.notifyClipboardKeyManagerRepaint()
	return nil
}

func (a *AppController) setClipboardActionStatus(status, errMsg string) {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	st.lastStatus = status
	st.lastError = errMsg
	st.mu.Unlock()
}

func (a *AppController) clipboardActionStatus() (string, string) {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.lastStatus, st.lastError
}

func (a *AppController) notifyClipboardKeyManagerRepaint() {
	a.mu.Lock()
	repaint := a.OnRepaint
	a.mu.Unlock()
	if repaint != nil {
		repaint()
	}
}

func clipboardActionSuccessLabel(action config.ClipboardActionKind) string {
	zh := i18n.IsChinese()
	switch action {
	case config.ClipboardActionCopyID:
		return pickLang(zh, "已复制条目 ID", "Copied entry ID")
	case config.ClipboardActionCopyPath:
		return pickLang(zh, "已复制路径", "Copied path")
	case config.ClipboardActionCopyMetadata:
		return pickLang(zh, "已复制元数据（已脱敏）", "Copied metadata (redacted)")
	case config.ClipboardActionPaste:
		return pickLang(zh, "已触发粘贴", "Pasted")
	default:
		return pickLang(zh, "已复制内容", "Copied content")
	}
}
