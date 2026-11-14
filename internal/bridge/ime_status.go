package bridge

import (
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// MatchedRule reports which configured rule (if any) applies to app, without mutating the
// engine's per-app session state. It is safe to call from UI code for diagnostics.
func (e *IMESwitchDecisionEngine) MatchedRule(app AppIdentity) (config.IMERule, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if isSelfClavisApp(app) {
		return config.IMERule{}, false
	}
	return e.matchRule(app)
}

// ForegroundIMEStatus describes the IME state of the last non-Clavis foreground app, for the
// settings IME card (Clavis' own settings window is frontmost while the card renders).
type ForegroundIMEStatus struct {
	App         AppIdentity
	Source      host.InputSourceInfo
	Rule        config.IMERule
	RuleMatched bool
	AutoSwitch  bool
}

// ForegroundIMEReporter is implemented by bridges that track the last external foreground app.
// It is optional (type-asserted) so HostService and the darwin bridge stay unchanged.
type ForegroundIMEReporter interface {
	ForegroundIMEStatus() (ForegroundIMEStatus, bool)
}

// DescribeIMERuleTarget returns a short human label for a rule's target ("Code.exe", bundle ID,
// or the title/URL pattern) for diagnostics.
func DescribeIMERuleTarget(r config.IMERule) string {
	for _, s := range []string{r.ProcessName, r.BundleID, r.AppName} {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	if t := strings.TrimSpace(r.TitlePattern); t != "" {
		return "title~" + t
	}
	return strings.TrimSpace(r.URLPattern)
}
