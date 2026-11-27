package diag

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const redactedMarker = "[REDACTED]"

var (
	tokenPrefixRe = regexp.MustCompile(`\b(?:sk-ant-[A-Za-z0-9_-]{8,}|sk-[A-Za-z0-9_-]{8,}|AIza[0-9A-Za-z_-]{12,}|ghp_[A-Za-z0-9]{12,}|gho_[A-Za-z0-9]{12,}|xox[a-zA-Z]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{12,})\b`)
	bearerRe      = regexp.MustCompile(`(?i)\b(Bearer)\s+[A-Za-z0-9._~+/=-]{8,}`)
	kvSecretRe    = regexp.MustCompile(`(?i)\b(api_key|apikey|token|secret|password|authorization|recovery_key|master_key)\b(\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)`)
	unixUsersRe   = regexp.MustCompile(`(/Users/|/home/)[^/\s"':;]+`)
	winUsersRe    = regexp.MustCompile(`(?i)([A-Z]:\\Users\\)[^\\\s"':;]+`)
)

// RedactText scrubs known API key formats, Bearer tokens, and key-value secret
// assignments from log lines, crash traces, or diagnostic text.
func RedactText(s string) string {
	if s == "" {
		return ""
	}
	out := tokenPrefixRe.ReplaceAllString(s, redactedMarker)
	out = bearerRe.ReplaceAllString(out, `$1 `+redactedMarker)
	out = kvSecretRe.ReplaceAllString(out, `$1$2`+redactedMarker)
	return out
}

// RedactPaths replaces the current home directory and OS user home prefixes
// (/Users/<name>, /home/<name>, C:\Users\<name>) with "~" so diagnostics never
// leak local usernames.
func RedactPaths(s string) string {
	if s == "" {
		return ""
	}
	out := s
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		cleanHome := filepath.Clean(home)
		slashHome := filepath.ToSlash(cleanHome)
		out = strings.ReplaceAll(out, cleanHome, "~")
		if slashHome != cleanHome {
			out = strings.ReplaceAll(out, slashHome, "~")
		}
	}
	out = unixUsersRe.ReplaceAllString(out, "~")
	out = winUsersRe.ReplaceAllString(out, "~")
	return out
}

// IsSensitiveFileName reports whether a file basename must always be excluded
// from unencrypted diagnostic bundles and redacted from plaintext logs.
func IsSensitiveFileName(name string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(name)))
	switch base {
	case "ai-keys.toml", "vault_secrets.enc", "vault.key":
		return true
	}
	for _, ext := range []string{".key", ".pem", ".p12", ".enc"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	for _, dbPrefix := range []string{"clipboard.db", "sync-state.db", "device_state.db"} {
		if strings.HasPrefix(base, dbPrefix) {
			return true
		}
	}
	return false
}
