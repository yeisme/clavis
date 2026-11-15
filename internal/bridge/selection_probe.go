package bridge

import (
	"bytes"
	"context"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// SelectionProber is implemented by bridges that can read the focused app's
// selection by copying it, for when the accessibility API cannot
// (host.ContextSnapshot.SelectionKnown is false). The pasteboard is put back
// as it was. Each platform decides where sending its copy shortcut is safe:
// on Windows, Ctrl+C in a console with nothing selected interrupts the
// running program; on macOS, ⌘C never does.
type SelectionProber interface {
	ProbeSelection(ctx context.Context, budget time.Duration) (string, error)
}

// CopyProbe captures a selection through the pasteboard: it snapshots the
// pasteboard, sends the platform's copy shortcut, waits up to a budget for
// the frontmost app to write its selection, reads it, and restores the
// snapshot. Platforms supply the shortcut and their pasteboard backend.
type CopyProbe struct {
	Backend PasteboardBackend
	// SendCopy posts the copy shortcut, tagged as Clavis's own input.
	SendCopy func()
	// Ignore tells the clipboard history to skip a change count: the copy
	// and the restore are not the user's.
	Ignore func(changeCount int64)
	// LateWatch is how long a copy that lands after the budget is still
	// watched for, so that the pasteboard is put back even then.
	LateWatch time.Duration

	poll  time.Duration
	sleep func(time.Duration)
	now   func() time.Time
}

// maxProbeText bounds what a probe returns: a selection, not a document.
const maxProbeText = 64 << 10

// Probe returns the selection the frontmost app copied, or "" when it
// copied nothing within budget (usually: nothing is selected) or copied the
// line under the caret because nothing was selected.
func (p *CopyProbe) Probe(ctx context.Context, budget time.Duration) (string, error) {
	poll, sleep, now := p.poll, p.sleep, p.now
	if poll <= 0 {
		poll = 4 * time.Millisecond
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	if now == nil {
		now = time.Now
	}
	ignore := p.Ignore
	if ignore == nil {
		ignore = func(int64) {}
	}

	saved, err := p.Backend.Capture()
	if err != nil {
		return "", err // without a snapshot the copy could not be undone
	}
	before := saved.ChangeCount
	// The copy's change count is ignored once it is seen, not up front: an
	// ignored count that never comes would hide the user's next real copy
	// from the clipboard history.
	p.SendCopy()

	for deadline := now().Add(budget); ; {
		if cc := p.Backend.ChangeCount(); cc != before {
			return p.takeCopy(saved, ignore)
		}
		if ctx.Err() != nil || !now().Before(deadline) {
			break
		}
		sleep(poll)
	}
	if p.LateWatch > 0 {
		go p.restoreLate(saved, ignore, sleep, now)
	}
	return "", nil
}

// takeCopy reads what the app copied and puts the saved pasteboard back.
func (p *CopyProbe) takeCopy(saved PasteboardSnapshot, ignore func(int64)) (string, error) {
	copied, err := p.Backend.Capture()
	if err != nil {
		return "", err
	}
	ignore(copied.ChangeCount)
	return copiedSelectionText(copied), p.restore(saved, copied.ChangeCount, ignore)
}

// restore puts saved back unless the pasteboard moved past expected, and
// keeps the restore itself out of the clipboard history.
func (p *CopyProbe) restore(saved PasteboardSnapshot, expected int64, ignore func(int64)) error {
	if p.Backend.ChangeCount() != expected {
		return nil // someone else wrote since: their content stays
	}
	ignore(expected + 1)
	_, err := NewPasteboardSnapshotRestorer(p.Backend).RestoreIfUnmodified(saved, expected)
	return err
}

// restoreLate puts the pasteboard back if a slow app's copy lands after the
// probe gave up. Only a single change counts as the copy: more mean someone
// else wrote too, and their content must stay.
func (p *CopyProbe) restoreLate(saved PasteboardSnapshot, ignore func(int64), sleep func(time.Duration), now func() time.Time) {
	poll := p.poll
	if poll <= 0 {
		poll = 10 * time.Millisecond
	}
	for deadline := now().Add(p.LateWatch); now().Before(deadline); sleep(poll) {
		cc := p.Backend.ChangeCount()
		if cc == saved.ChangeCount {
			continue
		}
		if cc == saved.ChangeCount+1 {
			ignore(cc)
			_ = p.restore(saved, cc, ignore)
		}
		return
	}
}

// copiedSelectionText is the text a probe copied, or "" when what the app
// copied is not a text selection: the line under the caret, which VS Code
// copies when nothing is selected and marks with isFromEmptySelection in its
// vscode-editor-data flavor, or files (Finder, Explorer), whose names come
// along as text.
func copiedSelectionText(s PasteboardSnapshot) string {
	for _, item := range s.Items {
		for _, rep := range item.Representations {
			switch {
			case rep.Type == "vscode-editor-data" && bytes.Contains(rep.Data, []byte(`"isFromEmptySelection":true`)):
				return ""
			case rep.Type == "public.file-url", rep.Type == "15": // 15: CF_HDROP
				return ""
			}
		}
	}
	return PasteboardSnapshotText(s)
}

// PasteboardSnapshotText returns the plain text a pasteboard snapshot holds:
// UTF-8 text on macOS (public.utf8-plain-text), UTF-16 text on Windows
// (CF_UNICODETEXT, format 13, NUL-terminated). The first item's text wins.
func PasteboardSnapshotText(s PasteboardSnapshot) string {
	for _, item := range s.Items {
		for _, rep := range item.Representations {
			var text string
			switch rep.Type {
			case "public.utf8-plain-text", "NSStringPboardType":
				text = string(rep.Data)
			case "13": // CF_UNICODETEXT
				text = decodeUTF16LE(rep.Data)
			default:
				continue
			}
			if !utf8.ValidString(text) {
				text = string(bytes.ToValidUTF8([]byte(text), []byte("�")))
			}
			if len(text) > maxProbeText {
				text = truncateUTF8(text, maxProbeText)
			}
			return text
		}
	}
	return ""
}

// decodeUTF16LE decodes little-endian UTF-16 up to the first NUL.
func decodeUTF16LE(b []byte) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u := uint16(b[i]) | uint16(b[i+1])<<8
		if u == 0 {
			break
		}
		units = append(units, u)
	}
	return string(utf16.Decode(units))
}
