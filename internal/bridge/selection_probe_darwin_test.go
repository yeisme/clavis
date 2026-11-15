//go:build darwin

package bridge

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// TestDarwinCopyProbeOnRealPasteboard runs CopyProbe against the real
// NSPasteboard: the "app" answers ⌘C by writing its selection a little
// later, as a real app does from its own event loop. The probe must return
// that text and put back every flavor of the clipboard it found. It touches
// the user's clipboard (and restores it), so it only runs when asked:
//
//	CLAVIS_PASTEBOARD_IT=1 go test -run TestDarwinCopyProbe ./internal/bridge/
func TestDarwinCopyProbeOnRealPasteboard(t *testing.T) {
	if os.Getenv("CLAVIS_PASTEBOARD_IT") == "" {
		t.Skip("touches the real pasteboard; set CLAVIS_PASTEBOARD_IT=1")
	}
	if err := initDarwin(); err != nil {
		t.Skipf("no AppKit: %v", err)
	}
	b := &darwinBridge{pbTracker: newSelfPasteboardTracker()}
	backend := &darwinPasteboardBackend{bridge: b}
	userClip, err := backend.Capture()
	if err != nil {
		t.Fatalf("capture the user's clipboard: %v", err)
	}
	defer func() { _ = backend.Restore(userClip) }()

	private := []byte{0xC1, 0xA0, 0x15, 0x00, 0xFF}
	original := PasteboardSnapshot{Items: []PasteboardItemSnapshot{{Representations: []PasteboardRepresentation{
		{Type: "public.utf8-plain-text", Data: []byte("clavis-it: the user's clipboard")},
		{Type: "com.yeisme.clavis.it-private", Data: private},
	}}}}
	if err := backend.Restore(original); err != nil {
		t.Fatalf("seed the clipboard: %v", err)
	}

	const selection = "clavis-it: 选中的一段文字 with emoji 👍"
	probe := CopyProbe{
		Backend: backend,
		SendCopy: func() {
			go func() {
				time.Sleep(15 * time.Millisecond)
				writeDarwinPasteboardString(selection)
			}()
		},
		Ignore: b.IgnoreSelfPasteboardChange,
	}
	start := time.Now()
	got, err := probe.Probe(context.Background(), 300*time.Millisecond)
	took := time.Since(start)
	if err != nil || got != selection {
		t.Fatalf("Probe = %q, %v; want the copied selection", got, err)
	}

	after, err := backend.Capture()
	if err != nil {
		t.Fatalf("capture after: %v", err)
	}
	if text := PasteboardSnapshotText(after); text != "clavis-it: the user's clipboard" {
		t.Fatalf("clipboard text after the probe = %q, want the original", text)
	}
	if !snapshotHas(after, "com.yeisme.clavis.it-private", private) {
		t.Fatalf("the private flavor was not restored: %+v", after.Items)
	}
	t.Logf("probe answered in %v; clipboard restored with %d flavor(s)", took, len(after.Items[0].Representations))

	// Nothing selected: the app copies nothing, the probe gives up at its
	// budget and the clipboard keeps its change count.
	cc := backend.ChangeCount()
	empty := CopyProbe{Backend: backend, SendCopy: func() {}, Ignore: b.IgnoreSelfPasteboardChange}
	if got, _ := empty.Probe(context.Background(), 40*time.Millisecond); got != "" || backend.ChangeCount() != cc {
		t.Fatalf("an empty probe returned %q and moved the change count %d → %d", got, cc, backend.ChangeCount())
	}
}

// TestDarwinProbeSelectionNeedsTrust: without Accessibility trust ⌘C could
// not be posted, so ProbeSelection must not wait out a budget or touch the
// clipboard.
func TestDarwinProbeSelectionNeedsTrust(t *testing.T) {
	if err := initDarwin(); err != nil {
		t.Skipf("no AppKit: %v", err)
	}
	if axIsProcessTrusted() {
		t.Skip("this process is trusted for Accessibility; ProbeSelection would post ⌘C to the frontmost app")
	}
	b := &darwinBridge{pbTracker: newSelfPasteboardTracker()}
	cc := (&darwinPasteboardBackend{bridge: b}).ChangeCount()
	start := time.Now()
	got, err := b.ProbeSelection(context.Background(), time.Second)
	if err != nil || got != "" {
		t.Fatalf("ProbeSelection untrusted = %q, %v", got, err)
	}
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Fatalf("an untrusted probe waited %v", took)
	}
	if now := (&darwinPasteboardBackend{bridge: b}).ChangeCount(); now != cc {
		t.Fatalf("an untrusted probe changed the clipboard (%d → %d)", cc, now)
	}
}

func snapshotHas(s PasteboardSnapshot, typ string, data []byte) bool {
	for _, item := range s.Items {
		for _, rep := range item.Representations {
			if rep.Type == typ && bytes.Equal(rep.Data, data) {
				return true
			}
		}
	}
	return false
}

// TestAXRoleHoldsNoText: Finder's file list and other non-text focus skip
// the probe; text areas, web areas and unknown roles are probed.
func TestAXRoleHoldsNoText(t *testing.T) {
	for _, role := range []string{"AXOutline", "AXList", "AXTable", "AXBrowser", "AXButton"} {
		if !axRoleHoldsNoText(role) {
			t.Errorf("%s should not be probed", role)
		}
	}
	for _, role := range []string{"AXTextArea", "AXTextField", "AXWebArea", "AXGroup", "AXScrollArea", "AXStaticText", ""} {
		if axRoleHoldsNoText(role) {
			t.Errorf("%s may hold a text selection and should be probed", role)
		}
	}
}
