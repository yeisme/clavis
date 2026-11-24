package uiapp

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type vaultStatusKind string

const (
	vaultStatusNone    vaultStatusKind = ""
	vaultStatusStored  vaultStatusKind = "stored"
	vaultStatusCopied  vaultStatusKind = "copied"
	vaultStatusLocked  vaultStatusKind = "locked"
	vaultStatusMissing vaultStatusKind = "missing"
	vaultStatusDenied  vaultStatusKind = "denied"
)

// classifyVaultFailure maps Vault copy/store errors into the three explicit UI
// error/degraded states (locked, missing, denied) without ever exposing secrets.
func classifyVaultFailure(receipt secrets.CopyReceipt, err error, zh bool) (vaultStatusKind, string, string) {
	code := strings.TrimSpace(receipt.ErrorCode)
	errLower := ""
	if err != nil {
		errLower = strings.ToLower(err.Error())
	}

	switch {
	case code == secrets.CopyErrorRefNotFound ||
		errors.Is(err, secrets.ErrSecretNotFound) ||
		errors.Is(err, os.ErrNotExist) ||
		(code == "" && (strings.Contains(errLower, "not_found") || strings.Contains(errLower, "not found") || strings.Contains(errLower, "missing"))):
		resolvedCode := nonEmptyOr(code, secrets.CopyErrorRefNotFound)
		if errors.Is(err, secrets.ErrSecretNotFound) && resolvedCode == secrets.CopyErrorKeychainLocked {
			resolvedCode = secrets.CopyErrorRefNotFound
		}
		return vaultStatusMissing, resolvedCode, pickLang(zh,
			"未找到对应的 SecretRef 或底层凭据已被移除，请重新写入凭据引用。",
			"Target SecretRef or backing credential is missing; re-add the credential reference.",
		)

	case code == secrets.CopyErrorAgentForbidden ||
		errors.Is(err, secrets.ErrAgentForbidden) ||
		code == secrets.CopyErrorRevoked ||
		strings.Contains(errLower, "agent_forbidden") ||
		strings.Contains(errLower, "revoked") ||
		strings.Contains(errLower, "forbidden") ||
		strings.Contains(errLower, "denied"):
		resolvedCode := nonEmptyOr(code, secrets.CopyErrorAgentForbidden)
		return vaultStatusDenied, resolvedCode, pickLang(zh,
			"访问被拒绝：Agent/自动化路径不可获取 Copy，或该 SecretRef 已被吊销。",
			"Access denied: Agent/automation paths cannot execute Copy, or this SecretRef is revoked.",
		)

	case code == secrets.CopyErrorKeychainLocked ||
		code == secrets.CopyErrorUserAuth ||
		strings.Contains(errLower, "lock"):
		resolvedCode := nonEmptyOr(code, secrets.CopyErrorKeychainLocked)
		return vaultStatusLocked, resolvedCode, pickLang(zh,
			"钥匙串已锁定或需要用户身份验证；解锁后请重试 TTL 复制。",
			"Keychain is locked or requires user verification; unlock keychain and retry TTL Copy.",
		)

	default:
		if !receipt.Retryable && err != nil {
			return vaultStatusDenied, nonEmptyOr(code, "access_denied"), pickLang(zh,
				"操作未获得授权，仅允许活跃用户会话执行短时复制。",
				"Operation denied; only an active user session may copy from Vault.",
			)
		}
		return vaultStatusLocked, nonEmptyOr(code, secrets.CopyErrorKeychainLocked), pickLang(zh,
			"安全存储暂不可用或处于锁定状态，请解锁后重试。",
			"Secret store is currently locked or unavailable; retry after unlocking.",
		)
	}
}

func (a *AppController) renderSettingsSecuritySection(c *ui.Context, theme *ui.Theme) {
	zh := i18n.IsChinese()
	ttlState := a.SensitiveClipboardState()
	km := a.clipboardKeyManager()
	km.mu.Lock()
	policyTTL := km.policy.SensitiveTTLSeconds
	km.mu.Unlock()
	if policyTTL <= 0 {
		policyTTL = int(secrets.DefaultSecretCopyTTL.Seconds())
	}

	settingsSourceCard(c, theme, pickLang(zh, "安全边界与 User-Only 通道", "Security Boundary & User-Only Channel"), "[vault]", func() {
		ui.Column(c).FillWidth().Gap(space6).Children(func() {
			ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
				if ttlState.Active {
					remSec := max(1, int(ttlState.Remaining.Seconds()+0.9))
					kitStatusPill(c, theme, pickLang(zh,
						fmt.Sprintf("User-Only TTL 生效中 (%ds)", remSec),
						fmt.Sprintf("User-Only TTL Active (%ds)", remSec),
					), chipWarning)
					clearLbl := pickLang(zh, "立即清空剪贴板", "Clear Clipboard Now")
					if ui.Button(c, clearLbl).Key("security-clear-ttl").Label(clearLbl).Padding(space2, space8).Clicked() {
						_ = a.ClearSensitiveClipboardNow()
					}
				} else {
					kitStatusPill(c, theme, pickLang(zh,
						fmt.Sprintf("User-Only TTL 通道就绪 (%ds)", policyTTL),
						fmt.Sprintf("User-Only TTL Ready (%ds)", policyTTL),
					), chipSuccess)
				}
				kitStatusPill(c, theme, pickLang(zh,
					"Agent 复制已封锁 (agent_forbidden)",
					"Agent Copy Blocked (agent_forbidden)",
				), chipNeutral)
			})

			ui.Text(c, pickLang(zh,
				"列表与审计仅展示 SecretRef 元数据；凭据正文不进入 Omnibox、对话记录、诊断快照、截图或星号预览，复制仅通过 User-Only 短时 TTL 通道完成并在过期或所有权变更后自动清除。",
				"Lists and audits show SecretRef metadata only; secret plaintext never enters Omnibox items, transcripts, diagnostics, screenshots, or asterisk previews, and copies execute strictly through the User-Only TTL channel.",
			)).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
		})
	})

	settingsSourceCard(c, theme, pickLang(zh, "密码与凭据引用", "Passwords & SecretRefs"), "vault_refs.json", func() {
		a.renderVaultBody(c, theme, true)
	})
}

func renderVaultStatusCard(c *ui.Context, theme *ui.Theme, st *vaultViewState, kind vaultStatusKind, code, msg string, zh bool) {
	if kind == vaultStatusNone || (msg == "" && code == "") {
		return
	}
	if kind == vaultStatusCopied || kind == vaultStatusStored {
		ui.Row(c).Key("vault-status-ok").FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			kitStatusPill(c, theme, pickLang(zh, "已完成", "OK"), chipSuccess)
			ui.Text(c, msg).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap().Grow(1)
		})
		return
	}

	var stateBadge string
	var stateTone chipTone
	switch kind {
	case vaultStatusLocked:
		stateBadge = pickLang(zh, "已锁定 · Locked", "Locked")
		stateTone = chipWarning
	case vaultStatusMissing:
		stateBadge = pickLang(zh, "引用缺失 · Missing", "Missing")
		stateTone = chipWarning
	case vaultStatusDenied:
		stateBadge = pickLang(zh, "访问被拒绝 · Denied", "Denied")
		stateTone = chipDanger
	default:
		stateBadge = pickLang(zh, "异常", "Warning")
		stateTone = chipWarning
	}

	kitCard(c, theme, cardInset, func() {
		ui.Column(c).Key("vault-status-"+string(kind)).FillWidth().Padding(space8, space10).Gap(space6).Children(func() {
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				kitStatusPill(c, theme, stateBadge, stateTone)
				if code != "" {
					kitStatusPill(c, theme, code, chipNeutral)
				}
				ui.Spacer(c)
				if kind == vaultStatusMissing {
					readdLbl := pickLang(zh, "重新录入", "Re-add")
					if ui.Button(c, readdLbl).Key("vault-status-readd").Label(readdLbl).Padding(space2, space8).Clicked() {
						st.mu.Lock()
						st.addOpen = true
						st.mu.Unlock()
					}
				}
				dismissLbl := pickLang(zh, "关闭", "Dismiss")
				if ui.Button(c, dismissLbl).Key("vault-status-dismiss").Label(dismissLbl).Padding(space2, space8).Clicked() {
					st.mu.Lock()
					st.clearStatusLocked()
					st.mu.Unlock()
				}
			})
			if msg != "" {
				ui.Text(c, msg).FontSize(typeMicro).TextColor(theme.Text).Wrap()
			}
		})
	})
}
