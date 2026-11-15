//go:build darwin

package bridge

import (
	"context"
	"os"
	"time"

	"github.com/ebitengine/purego/objc"
)

// selectionLateWatch is how long after a probe gives up a slow app's copy is
// still undone (see CopyProbe.LateWatch).
const selectionLateWatch = 500 * time.Millisecond

// ProbeSelection reads the frontmost app's selection by copying it with ⌘C,
// for apps whose accessibility tree does not answer AXSelectedText
// (Electron apps, Chromium web content, Java apps). The pasteboard is put
// back as it was. It does nothing without Accessibility trust (the shortcut
// could not be posted), under secure input (a password field refuses to
// copy), or when Clavis itself is frontmost.
func (b *darwinBridge) ProbeSelection(ctx context.Context, budget time.Duration) (string, error) {
	if err := initDarwin(); err != nil {
		return "", err
	}
	if !axIsProcessTrusted() || isSecureEventInputEnabled() || darwinSelfIsFrontmost() {
		return "", nil
	}
	p := CopyProbe{
		Backend:   &darwinPasteboardBackend{bridge: b},
		SendCopy:  func() { postTaggedKey(kVK_ANSI_C, kCGEventFlagMaskCommand) },
		Ignore:    b.IgnoreSelfPasteboardChange,
		LateWatch: selectionLateWatch,
	}
	return p.Probe(ctx, budget)
}

// axRoleHoldsNoText reports whether a focused element of this AX role can
// hold no text selection (lists, tables, buttons): its selection is known
// to be empty without a probe.
func axRoleHoldsNoText(role string) bool {
	switch role {
	case "AXOutline", "AXList", "AXTable", "AXBrowser", "AXRow", "AXCell",
		"AXButton", "AXCheckBox", "AXRadioButton", "AXPopUpButton", "AXMenuButton",
		"AXMenu", "AXMenuBar", "AXMenuItem", "AXImage", "AXSlider", "AXScrollBar",
		"AXTabGroup", "AXToolbar", "AXDisclosureTriangle":
		return true
	}
	return false
}

// darwinSelfIsFrontmost reports whether Clavis's own process is the
// frontmost app, where a probe would copy from Clavis's windows.
func darwinSelfIsFrontmost() bool {
	self := false
	withAutoreleasePool(func() {
		ws := cls("NSWorkspace").Send(sel("sharedWorkspace"))
		if ws == 0 {
			return
		}
		front := ws.Send(sel("frontmostApplication"))
		if front == 0 {
			return
		}
		self = int(objc.Send[int32](front, sel("processIdentifier"))) == os.Getpid()
	})
	return self
}

var _ SelectionProber = (*darwinBridge)(nil)
