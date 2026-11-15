package uiapp

import "github.com/yeisme/clavis/pkg/core/i18n"

// aiModeLabel is the display name of an AI mode ID: the stock modes
// (polish, translate, the ad-hoc "custom" ask) are localized, user-defined
// modes show their own ID, and an empty mode reads as a plain ask.
func aiModeLabel(mode string) string {
	switch mode {
	case "":
		return i18n.T("ai.mode.ask")
	case "custom":
		return i18n.T("ai.mode.custom")
	case "polish":
		return i18n.T("ai.mode.polish")
	case "translate":
		return i18n.T("ai.mode.translate")
	}
	return mode
}

// asciiLockedForCapsule reports whether the open Leader/Capsule session holds
// the temporary ASCII input lock (ime.lock_ascii_on_leader), so the view can
// tell the user why keys type Latin letters while a Chinese IME was active.
func (c *AppController) asciiLockedForCapsule() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imeLockedForCapsule
}
