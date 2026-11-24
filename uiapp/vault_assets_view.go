package uiapp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type hostVaultClipboard struct {
	ctrl  *AppController
	mu    sync.Mutex
	token string
}

func (a *hostVaultClipboard) Write(value string) (string, error) {
	if a == nil || a.ctrl == nil {
		return "", errors.New("clipboard adapter unavailable")
	}
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	token := "vtok-" + hex.EncodeToString(rnd[:])

	st := a.ctrl.clipboardKeyManager()
	hash := hashClipboardPayload(value)
	now := time.Now()

	st.mu.Lock()
	if st.recentSecretHashes == nil {
		st.recentSecretHashes = make(map[string]time.Time)
	}
	st.recentSecretHashes[hash] = now.Add(secrets.MaxSecretCopyTTL + 5*time.Second)
	st.mu.Unlock()

	if err := a.ctrl.writeClipboardText(value); err != nil {
		return "", err
	}

	a.mu.Lock()
	a.token = token
	a.mu.Unlock()
	return token, nil
}

func (a *hostVaultClipboard) ClearIfOwned(token string) error {
	if a == nil || a.ctrl == nil {
		return errors.New("clipboard adapter unavailable")
	}
	a.mu.Lock()
	owned := a.token != "" && a.token == token
	if owned {
		a.token = ""
	}
	a.mu.Unlock()
	if !owned {
		return secrets.ErrOwnershipLost
	}

	st := a.ctrl.clipboardKeyManager()
	st.mu.Lock()
	hook := st.clearHook
	st.mu.Unlock()
	if hook != nil {
		return hook()
	}
	return a.ctrl.writeClipboardText("")
}

func (c *AppController) initSecretVault(custom *secrets.Vault) {
	if c == nil {
		return
	}
	c.vaultMu.Lock()
	defer c.vaultMu.Unlock()

	c.vaultAudit = &secrets.MemoryAuditSink{}
	if c.ConfigStore != nil && strings.TrimSpace(c.ConfigStore.Directory()) != "" {
		c.vaultMetaPath = filepath.Join(c.ConfigStore.Directory(), "vault_refs.json")
	}
	if custom != nil {
		c.vault = custom
	} else {
		store := c.SecretStore
		if store == nil {
			store = secrets.NewMemoryAPIKeyStore()
		}
		c.vault = secrets.NewVault(
			secrets.NewLayeredSecretStore(store),
			&hostVaultClipboard{ctrl: c},
			c.vaultAudit,
		)
	}
	c.loadVaultMetadataLocked()
}

func (c *AppController) SecretVault() *secrets.Vault {
	c.vaultMu.Lock()
	if c.vault == nil {
		c.vaultMu.Unlock()
		c.initSecretVault(nil)
		c.vaultMu.Lock()
	}
	v := c.vault
	c.vaultMu.Unlock()
	return v
}

// VaultSnapshot returns the metadata-only SecretRef list and CopyReceipts (never any plaintext).
func (c *AppController) VaultSnapshot() secrets.AgentVaultSnapshot {
	snap := c.SecretVault().Snapshot()
	sort.Slice(snap.SecretRefs, func(i, j int) bool {
		if snap.SecretRefs[i].State != snap.SecretRefs[j].State {
			iStored := snap.SecretRefs[i].State == secrets.SecretStateStored
			jStored := snap.SecretRefs[j].State == secrets.SecretStateStored
			if iStored != jStored {
				return iStored
			}
			return snap.SecretRefs[i].State < snap.SecretRefs[j].State
		}
		if !snap.SecretRefs[i].UpdatedAt.Equal(snap.SecretRefs[j].UpdatedAt) {
			return snap.SecretRefs[i].UpdatedAt.After(snap.SecretRefs[j].UpdatedAt)
		}
		return snap.SecretRefs[i].ID < snap.SecretRefs[j].ID
	})
	return snap
}

// VaultAuditEvents returns buffered metadata-only Vault audit events.
func (c *AppController) VaultAuditEvents() []secrets.AuditEvent {
	c.vaultMu.Lock()
	audit := c.vaultAudit
	c.vaultMu.Unlock()
	if audit == nil {
		return nil
	}
	return audit.SnapshotEvents()
}

func (c *AppController) loadVaultMetadataLocked() {
	if c.vault == nil || strings.TrimSpace(c.vaultMetaPath) == "" {
		return
	}
	data, err := os.ReadFile(c.vaultMetaPath)
	if err != nil {
		return
	}
	var refs []secrets.SecretRef
	if json.Unmarshal(data, &refs) != nil {
		return
	}
	for _, ref := range refs {
		_ = c.vault.RestoreRef(ref)
	}
}

func (c *AppController) persistVaultMetadata() {
	c.vaultMu.Lock()
	v := c.vault
	path := c.vaultMetaPath
	c.vaultMu.Unlock()
	if v == nil || strings.TrimSpace(path) == "" {
		return
	}
	snap := v.Snapshot()
	raw, err := json.MarshalIndent(snap.SecretRefs, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, raw, 0o600)
}

// VaultPutSecret stores a secret in the trusted SecretValueStore and records metadata-only SecretRef.
func (c *AppController) VaultPutSecret(id, recordKey, label, kind, value string) (secrets.SecretRef, error) {
	v := c.SecretVault()
	ref := secrets.NewSecretRef(strings.TrimSpace(id), strings.TrimSpace(recordKey), strings.TrimSpace(label))
	if strings.TrimSpace(kind) != "" {
		ref.Kind = strings.TrimSpace(kind)
	}
	if old, err := v.Metadata(ref.ID); err == nil {
		ref.Version = old.Version
	}
	stored, err := v.Put(ref, value)
	if err != nil {
		return secrets.SecretRef{}, err
	}
	c.persistVaultMetadata()
	return stored, nil
}

// VaultUserCopy executes a User-Only TTL-protected copy from Vault and syncs Header TTL pill.
func (c *AppController) VaultUserCopy(secretRefID string) (secrets.CopyReceipt, error) {
	return c.VaultCopyAs(secrets.ActorUserSession, "clavis-ui-user", secretRefID)
}

// VaultAgentCopyAttempt simulates/executes an Agent copy request so all agent paths
// go through Vault.Copy and deterministically receive ErrAgentForbidden without copy.
func (c *AppController) VaultAgentCopyAttempt(secretRefID string) (secrets.CopyReceipt, error) {
	return c.VaultCopyAs(secrets.ActorAgent, "clavis-agent-session", secretRefID)
}

// VaultCopyAs executes a Vault.Copy request under the supplied actor identity and
// updates Vault UI status classification (copied, locked, missing, or denied).
func (c *AppController) VaultCopyAs(actor, sessionID, secretRefID string) (secrets.CopyReceipt, error) {
	v := c.SecretVault()
	st := c.clipboardKeyManager()
	st.mu.Lock()
	ttlSec := st.policy.SensitiveTTLSeconds
	st.mu.Unlock()
	ttl := time.Duration(ttlSec) * time.Second
	if ttl <= 0 {
		ttl = secrets.DefaultSecretCopyTTL
	}
	if ttl > secrets.MaxSecretCopyTTL {
		ttl = secrets.MaxSecretCopyTTL
	}

	reqID := fmt.Sprintf("vreq-%d", time.Now().UnixNano())
	receipt, err := v.Copy(secrets.UserCopyRequest{
		RequestID:   reqID,
		SecretRefID: strings.TrimSpace(secretRefID),
		Actor:       actor,
		SessionID:   strings.TrimSpace(sessionID),
		TTL:         ttl,
		Destination: secrets.CopyDestinationClipboard,
		Reason:      "user_vault_copy",
	})
	zh := i18n.IsChinese()
	uiSt := vaultUIState(c)
	if err != nil {
		kind, code, detail := classifyVaultFailure(receipt, err, zh)
		uiSt.mu.Lock()
		uiSt.setStatusLocked(kind, code, detail)
		uiSt.mu.Unlock()
		c.notifyClipboardKeyManagerRepaint()
		if actor == secrets.ActorUserSession {
			c.ShowToastOrHUD(detail)
		}
		return receipt, err
	}

	meta, _ := v.Metadata(secretRefID)
	label := strings.TrimSpace(meta.Label)
	if label == "" {
		label = strings.TrimSpace(meta.RecordKey)
	}
	if label == "" {
		label = "Vault Reference"
	}

	st.mu.Lock()
	if st.sensitiveTimer != nil {
		st.sensitiveTimer.Stop()
	}
	st.sensitiveGen++
	gen := st.sensitiveGen
	st.sensitiveActive = true
	st.sensitiveLabel = label
	st.sensitiveTTL = ttl
	st.sensitiveExpiresAt = receipt.ExpiresAt
	st.sensitiveTimer = time.AfterFunc(ttl, func() {
		_ = v.Cleanup(reqID, secretRefID, strings.TrimSpace(sessionID))
		c.clearSensitiveClipboardForGen(gen)
	})
	st.mu.Unlock()

	okMsg := fmt.Sprintf("%s (%ds TTL)", i18n.T("hud.copied"), int(ttl.Seconds()))
	uiSt.mu.Lock()
	uiSt.setStatusLocked(vaultStatusCopied, string(receipt.State), okMsg)
	uiSt.mu.Unlock()

	c.notifyClipboardKeyManagerRepaint()
	c.ShowToastOrHUD(okMsg)
	return receipt, nil
}

// VaultRevoke revokes a SecretRef so future copies fail safely and persists metadata.
func (c *AppController) VaultRevoke(secretRefID string) error {
	if err := c.SecretVault().Revoke(secretRefID); err != nil {
		uiSt := vaultUIState(c)
		kind, code, detail := classifyVaultFailure(secrets.CopyReceipt{ErrorCode: secrets.CopyErrorRefNotFound}, err, i18n.IsChinese())
		uiSt.mu.Lock()
		uiSt.setStatusLocked(kind, code, detail)
		uiSt.mu.Unlock()
		return err
	}
	c.persistVaultMetadata()
	return nil
}

type vaultViewState struct {
	mu          sync.Mutex
	addOpen     bool
	auditOpen   bool
	recordDraft string
	labelDraft  string
	valueDraft  string
	statusKind  vaultStatusKind
	statusCode  string
	statusMsg   string
}

func (s *vaultViewState) setStatusLocked(kind vaultStatusKind, code, msg string) {
	s.statusKind = kind
	s.statusCode = code
	s.statusMsg = msg
}

func (s *vaultViewState) clearStatusLocked() {
	s.statusKind = vaultStatusNone
	s.statusCode = ""
	s.statusMsg = ""
}

var vaultViewStateStore = struct {
	mu    sync.Mutex
	byCtl map[*AppController]*vaultViewState
}{
	byCtl: make(map[*AppController]*vaultViewState),
}

func vaultUIState(c *AppController) *vaultViewState {
	vaultViewStateStore.mu.Lock()
	defer vaultViewStateStore.mu.Unlock()
	st := vaultViewStateStore.byCtl[c]
	if st == nil {
		st = &vaultViewState{}
		vaultViewStateStore.byCtl[c] = st
	}
	return st
}

func (a *AppController) renderVaultAssetsView(c *ui.Context, theme *ui.Theme) {
	a.renderVaultBody(c, theme, false)
}

func (a *AppController) renderVaultBody(c *ui.Context, theme *ui.Theme, inlineInScroll bool) {
	zh := i18n.IsChinese()
	st := vaultUIState(a)
	snap := a.VaultSnapshot()
	ttlState := a.SensitiveClipboardState()
	events := a.VaultAuditEvents()

	st.mu.Lock()
	addOpen := st.addOpen
	auditOpen := st.auditOpen
	recDraft := st.recordDraft
	lblDraft := st.labelDraft
	valDraft := st.valueDraft
	statusKind := st.statusKind
	statusCode := st.statusCode
	statusMsg := st.statusMsg
	st.mu.Unlock()

	renderListAndAudit := func() {
		ui.Column(c).FillWidth().Gap(space4).Children(func() {
			if len(snap.SecretRefs) == 0 {
				kitStateView(c, theme, kitStateEmpty, "lock.rotation",
					pickLang(zh, "暂无 SecretRef 记录", "No SecretRef entries yet"),
					pickLang(zh, "点击「+ 新增引用」安全托管凭据，仅显示元数据与审计状态。", "Click '+ New Ref' to store credentials safely; only metadata is shown here."),
					nil)
			}
			for _, ref := range snap.SecretRefs {
				r := ref
				title := strings.TrimSpace(r.Label)
				if title == "" {
					title = r.RecordKey
				}
				kitCard(c, theme, cardFlat, func() {
					ui.Row(c).FillWidth().Padding(space8, space10).Gap(space8).AlignItems(ui.Center).Children(func() {
						renderSemanticIcon(c, "lock.rotation", typeBody, secondaryInk(theme))
						ui.Column(c).Grow(1).MinWidth(0).Gap(space2).Children(func() {
							ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, title).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
								tone := chipNeutral
								if r.State == secrets.SecretStateRevoked {
									tone = chipDanger
								}
								kitStatusPill(c, theme, r.State, tone)
							})
							metaLine := fmt.Sprintf("%s · v%d · %s", r.RecordKey, r.Version, r.Kind)
							if r.Backend != "" {
								metaLine += " · " + r.Backend
							}
							ui.Text(c, metaLine).
								FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
						})

						if r.State != secrets.SecretStateRevoked {
							copyLbl := pickLang(zh, "TTL 复制", "Copy")
							if ui.Button(c, copyLbl).Key("vault-copy-" + r.ID).Label(copyLbl).Padding(space2, space8).Clicked() {
								_, _ = a.VaultUserCopy(r.ID)
							}
							revLbl := pickLang(zh, "吊销", "Revoke")
							if ui.Button(c, revLbl).Key("vault-revoke-" + r.ID).Label(revLbl).Padding(space2, space8).Clicked() {
								_ = a.VaultRevoke(r.ID)
							}
						}
					})
				})
			}

			if auditOpen {
				kitDivider(c, theme)
				kitCard(c, theme, cardInset, func() {
					ui.Column(c).FillWidth().Padding(space8, space10).Gap(space4).Children(func() {
						ui.Text(c, pickLang(zh, "Vault 审计记录（零敏感数据）", "Vault Audit Log (Metadata Only)")).
							FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
						if len(events) == 0 {
							ui.Text(c, pickLang(zh, "暂无审计事件", "No audit events")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						}
						start := max(0, len(events)-10)
						for i := len(events) - 1; i >= start; i-- {
							ev := events[i]
							line := fmt.Sprintf("%s · %s · %s", ev.At.Format("15:04:05"), ev.Action, ev.State)
							if ev.Actor != "" {
								line += " · " + ev.Actor
							}
							if ev.ErrorCode != "" {
								line += " (" + ev.ErrorCode + ")"
							}
							ui.Text(c, line).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
						}
					})
				})
			}
		})
	}

	root := ui.Column(c).Key("vault-assets-view").FillWidth().Gap(space8)
	if !inlineInScroll {
		root = root.Grow(1).MinHeight(0)
	}
	root.Children(func() {
		// Top bar: status summary + Add Reference toggle + Audit drawer toggle.
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "lock.rotation", typeMeta, secondaryInk(theme))
			ui.Text(c, pickLang(zh,
				fmt.Sprintf("SecretRef 索引 (%d) · 仅用户 TTL 复制", len(snap.SecretRefs)),
				fmt.Sprintf("SecretRef Index (%d) · User-Only TTL Copy", len(snap.SecretRefs)),
			)).FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).SingleLine().Grow(1)

			if ttlState.Active {
				remSec := max(1, int(ttlState.Remaining.Seconds()+0.9))
				clearLbl := pickLang(zh, fmt.Sprintf("立即清空 (%ds)", remSec), fmt.Sprintf("Clear (%ds)", remSec))
				if ui.Button(c, clearLbl).Key("vault-clear-now").Label(clearLbl).Padding(space2, space8).Clicked() {
					_ = a.ClearSensitiveClipboardNow()
				}
			}

			addLbl := pickLang(zh, "+ 新增引用", "+ New Ref")
			if addOpen {
				addLbl = pickLang(zh, "收起表单", "Close Form")
			}
			if ui.Button(c, addLbl).Key("vault-toggle-add").Label(addLbl).Padding(space2, space8).Clicked() {
				st.mu.Lock()
				st.addOpen = !st.addOpen
				st.mu.Unlock()
			}

			auditLbl := pickLang(zh, fmt.Sprintf("审计 (%d)", len(events)), fmt.Sprintf("Audit (%d)", len(events)))
			if ui.Button(c, auditLbl).Key("vault-toggle-audit").Label(auditLbl).Padding(space2, space8).Clicked() {
				st.mu.Lock()
				st.auditOpen = !st.auditOpen
				st.mu.Unlock()
			}
		})

		renderVaultStatusCard(c, theme, st, statusKind, statusCode, statusMsg, zh)

		if addOpen {
			kitCard(c, theme, cardInset, func() {
				ui.Column(c).FillWidth().Padding(space8, space10).Gap(space6).Children(func() {
					ui.Text(c, pickLang(zh, "新增 Vault 引用（Label 请使用业务别名，禁止含 password/token/secret）", "New Vault Ref (avoid 'password'/'token'/'secret' in Label)")).
						FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).Wrap()

					ui.Row(c).FillWidth().Gap(space6).Children(func() {
						rkIn := ui.TextInput(c.Key("vault-in-record"), &recDraft).
							Label(pickLang(zh, "记录标识", "Record Key")).
							Placeholder(pickLang(zh, "例: clavis.prod.db", "e.g. clavis.prod.db")).Grow(1)
						if rkIn.Changed() {
							st.mu.Lock()
							st.recordDraft = recDraft
							st.mu.Unlock()
						}
						lbIn := ui.TextInput(c.Key("vault-in-label"), &lblDraft).
							Label(pickLang(zh, "业务别名", "Alias Label")).
							Placeholder(pickLang(zh, "例: Production DB", "e.g. Production DB")).Grow(1)
						if lbIn.Changed() {
							st.mu.Lock()
							st.labelDraft = lblDraft
							st.mu.Unlock()
						}
					})
					ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.End).Children(func() {
						vlIn := ui.TextInput(c.Key("vault-in-value"), &valDraft).Password().
							Label(pickLang(zh, "凭据正文", "Credential")).
							Placeholder(pickLang(zh, "提交后立即清空草稿", "Draft cleared on save")).Grow(1)
						if vlIn.Changed() {
							st.mu.Lock()
							st.valueDraft = valDraft
							st.mu.Unlock()
						}
						saveLbl := pickLang(zh, "保存", "Save")
						if ui.Button(c, saveLbl).Key("vault-save-btn").Padding(space2, space10).Clicked() {
							_, err := a.VaultPutSecret("", recDraft, lblDraft, secrets.SecretKindPassword, valDraft)
							st.mu.Lock()
							st.valueDraft = "" // wipe plaintext draft immediately
							if err != nil {
								kind, code, detail := classifyVaultFailure(secrets.CopyReceipt{}, err, zh)
								st.setStatusLocked(kind, code, detail)
							} else {
								st.recordDraft = ""
								st.labelDraft = ""
								st.addOpen = false
								st.setStatusLocked(vaultStatusStored, secrets.SecretStateStored, pickLang(zh, "已安全存入分层密钥库", "Stored in layered SecretStore"))
							}
							st.mu.Unlock()
						}
					})
				})
			})
		}

		// Metadata-only SecretRef list & optional Audit drawer.
		if inlineInScroll {
			renderListAndAudit()
		} else {
			ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(renderListAndAudit)
		}
	})
}
