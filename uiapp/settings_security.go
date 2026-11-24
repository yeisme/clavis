package uiapp

import (
	"errors"
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
	vaultStatusInvalid vaultStatusKind = "invalid"
)

// classifyVaultFailure maps Vault copy/store errors into explicit UI states
// (locked, missing, denied, invalid) without ever exposing secrets.
func classifyVaultFailure(receipt secrets.CopyReceipt, err error, _ bool) (vaultStatusKind, string, string) {
	code := strings.TrimSpace(receipt.ErrorCode)
	errLower := ""
	if err != nil {
		errLower = strings.ToLower(err.Error())
	}

	switch {
	case code == secrets.CopyErrorInvalidInput ||
		code == secrets.CopyErrorTTLInvalid ||
		code == secrets.CopyErrorVersionConflict ||
		errors.Is(err, secrets.ErrInvalidInput) ||
		errors.Is(err, secrets.ErrTTLInvalid) ||
		strings.Contains(errLower, secrets.CopyErrorInvalidInput) ||
		strings.Contains(errLower, "label may not") ||
		strings.Contains(errLower, "already exists") ||
		strings.Contains(errLower, "are required") ||
		strings.Contains(errLower, "is required") ||
		strings.Contains(errLower, "unsupported backend") ||
		strings.Contains(errLower, secrets.CopyErrorVersionConflict):
		resolvedCode := nonEmptyOr(code, secrets.CopyErrorInvalidInput)
		if errors.Is(err, secrets.ErrTTLInvalid) {
			resolvedCode = secrets.CopyErrorTTLInvalid
		} else if strings.Contains(errLower, secrets.CopyErrorVersionConflict) {
			resolvedCode = secrets.CopyErrorVersionConflict
		}
		return vaultStatusInvalid, resolvedCode, formatVaultInvalidInputDetail(errLower)

	case code == secrets.CopyErrorRefNotFound ||
		errors.Is(err, secrets.ErrSecretNotFound) ||
		errors.Is(err, os.ErrNotExist) ||
		(code == "" && (strings.Contains(errLower, "not_found") || strings.Contains(errLower, "not found") || strings.Contains(errLower, "missing"))):
		resolvedCode := nonEmptyOr(code, secrets.CopyErrorRefNotFound)
		if errors.Is(err, secrets.ErrSecretNotFound) && resolvedCode == secrets.CopyErrorKeychainLocked {
			resolvedCode = secrets.CopyErrorRefNotFound
		}
		return vaultStatusMissing, resolvedCode, i18n.T("vault.err.missing")

	case code == secrets.CopyErrorAgentForbidden ||
		errors.Is(err, secrets.ErrAgentForbidden) ||
		code == secrets.CopyErrorRevoked ||
		strings.Contains(errLower, "agent_forbidden") ||
		strings.Contains(errLower, "revoked") ||
		strings.Contains(errLower, "forbidden") ||
		strings.Contains(errLower, "denied"):
		resolvedCode := nonEmptyOr(code, secrets.CopyErrorAgentForbidden)
		return vaultStatusDenied, resolvedCode, i18n.T("vault.err.denied")

	case code == secrets.CopyErrorKeychainLocked ||
		code == secrets.CopyErrorUserAuth ||
		strings.Contains(errLower, "lock"):
		resolvedCode := nonEmptyOr(code, secrets.CopyErrorKeychainLocked)
		return vaultStatusLocked, resolvedCode, i18n.T("vault.err.locked")

	default:
		if !receipt.Retryable && err != nil {
			return vaultStatusDenied, nonEmptyOr(code, "access_denied"), i18n.T("vault.err.session_denied")
		}
		return vaultStatusLocked, nonEmptyOr(code, secrets.CopyErrorKeychainLocked), i18n.T("vault.err.unavailable")
	}
}

func formatVaultInvalidInputDetail(errLower string) string {
	switch {
	case strings.Contains(errLower, "label"):
		return i18n.T("vault.err.invalid_label")
	case strings.Contains(errLower, "duplicate") || strings.Contains(errLower, "already exists"):
		return i18n.T("vault.err.duplicate_key")
	case strings.Contains(errLower, "value is required"):
		return i18n.T("vault.err.value_required")
	case strings.Contains(errLower, "record_key"):
		return i18n.T("vault.err.record_key_required")
	case strings.Contains(errLower, secrets.CopyErrorTTLInvalid):
		return i18n.T("vault.err.ttl_invalid")
	case strings.Contains(errLower, secrets.CopyErrorVersionConflict):
		return i18n.T("vault.err.version_conflict")
	default:
		return i18n.T("vault.err.invalid_input")
	}
}

func (a *AppController) renderSettingsSecuritySection(c *ui.Context, theme *ui.Theme) {
	ttlState := a.SensitiveClipboardState()
	km := a.clipboardKeyManager()
	km.mu.Lock()
	policyTTL := km.policy.SensitiveTTLSeconds
	km.mu.Unlock()
	if policyTTL <= 0 {
		policyTTL = int(secrets.DefaultSecretCopyTTL.Seconds())
	}

	settingsSourceCard(c, theme, i18n.T("settings.security.card.boundary"), "[vault]", func() {
		ui.Column(c).FillWidth().Gap(space6).Children(func() {
			ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
				if ttlState.Active {
					remSec := max(1, int(ttlState.Remaining.Seconds()+0.9))
					kitStatusPill(c, theme, i18n.Tf("settings.security.ttl_active", remSec), chipWarning)
					clearLbl := i18n.T("settings.security.clear_clipboard_now")
					if ui.Button(c, clearLbl).Key("security-clear-ttl").Label(clearLbl).Padding(space2, space8).Clicked() {
						_ = a.ClearSensitiveClipboardNow()
					}
				} else {
					kitStatusPill(c, theme, i18n.Tf("settings.security.ttl_ready", policyTTL), chipSuccess)
				}
				kitStatusPill(c, theme, i18n.T("settings.security.agent_copy_blocked"), chipNeutral)
			})

			ui.Text(c, i18n.T("settings.security.boundary_desc")).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
		})
	})

	settingsSourceCard(c, theme, i18n.T("settings.security.card.vault"), "vault_refs.json", func() {
		a.renderVaultBody(c, theme, true)
	})
}

func renderVaultStatusCard(c *ui.Context, theme *ui.Theme, st *vaultViewState, kind vaultStatusKind, code, msg string, _ bool) {
	if kind == vaultStatusNone || (msg == "" && code == "") {
		return
	}
	if kind == vaultStatusCopied || kind == vaultStatusStored {
		ui.Row(c).Key("vault-status-ok").FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			kitStatusPill(c, theme, i18n.T("vault.status.ok"), chipSuccess)
			ui.Text(c, msg).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap().Grow(1)
		})
		return
	}

	var stateBadge string
	var stateTone chipTone
	switch kind {
	case vaultStatusLocked:
		stateBadge = i18n.T("vault.status.locked")
		stateTone = chipWarning
	case vaultStatusMissing:
		stateBadge = i18n.T("vault.status.missing")
		stateTone = chipWarning
	case vaultStatusDenied:
		stateBadge = i18n.T("vault.status.denied")
		stateTone = chipDanger
	case vaultStatusInvalid:
		stateBadge = i18n.T("vault.status.invalid")
		stateTone = chipWarning
	default:
		stateBadge = i18n.T("vault.status.warning")
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
					readdLbl := i18n.T("vault.action.readd")
					if ui.Button(c, readdLbl).Key("vault-status-readd").Label(readdLbl).Padding(space2, space8).Clicked() {
						st.mu.Lock()
						st.addOpen = true
						st.mu.Unlock()
					}
				}
				dismissLbl := i18n.T("vault.action.dismiss")
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
