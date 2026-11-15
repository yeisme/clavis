package bridge

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// fakePasteboard is an in-memory PasteboardBackend: every write and
// restore bumps the change count, as NSPasteboard and the Windows
// clipboard sequence number do.
type fakePasteboard struct {
	mu       sync.Mutex
	snap     PasteboardSnapshot
	restores int
}

func (f *fakePasteboard) ChangeCount() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap.ChangeCount
}

func (f *fakePasteboard) Capture() (PasteboardSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap.Clone(), nil
}

func (f *fakePasteboard) Restore(s PasteboardSnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cc := f.snap.ChangeCount + 1
	f.snap = s.Clone()
	f.snap.ChangeCount = cc
	f.restores++
	return nil
}

func (f *fakePasteboard) write(items ...PasteboardItemSnapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap = PasteboardSnapshot{ChangeCount: f.snap.ChangeCount + 1, Items: items}
}

func (f *fakePasteboard) items() []PasteboardItemSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap.Clone().Items
}

func (f *fakePasteboard) restoreCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restores
}

func textItem(s string) PasteboardItemSnapshot {
	return PasteboardItemSnapshot{Representations: []PasteboardRepresentation{
		{Type: "public.utf8-plain-text", Data: []byte(s)},
	}}
}

// userClipboard is what the user had copied before the probe: text plus a
// second flavor, which a restore must bring back byte for byte.
func userClipboard() []PasteboardItemSnapshot {
	return []PasteboardItemSnapshot{{Representations: []PasteboardRepresentation{
		{Type: "public.utf8-plain-text", Data: []byte("the user's clipboard")},
		{Type: "public.png", Data: []byte{0x89, 'P', 'N', 'G'}},
	}}}
}

type ignoreLog struct {
	mu  sync.Mutex
	got []int64
}

func (l *ignoreLog) add(cc int64) { l.mu.Lock(); l.got = append(l.got, cc); l.mu.Unlock() }
func (l *ignoreLog) list() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int64(nil), l.got...)
}

func newProbe(pb *fakePasteboard, send func(), ign *ignoreLog) *CopyProbe {
	return &CopyProbe{Backend: pb, SendCopy: send, Ignore: ign.add, poll: time.Millisecond}
}

func TestCopyProbeReadsSelectionAndRestores(t *testing.T) {
	pb := &fakePasteboard{snap: PasteboardSnapshot{ChangeCount: 40, Items: userClipboard()}}
	ign := &ignoreLog{}
	got, err := newProbe(pb, func() { pb.write(textItem("把这段话改得更通顺")) }, ign).
		Probe(context.Background(), 200*time.Millisecond)
	if err != nil || got != "把这段话改得更通顺" {
		t.Fatalf("Probe = %q, %v", got, err)
	}
	if !reflect.DeepEqual(pb.items(), userClipboard()) || pb.restoreCount() != 1 {
		t.Fatalf("clipboard not restored: %+v (restores %d)", pb.items(), pb.restoreCount())
	}
	// The copy (41) and the restore (42) stay out of the clipboard history.
	if want := []int64{41, 42}; !reflect.DeepEqual(ign.list(), want) {
		t.Fatalf("ignored change counts = %v, want %v", ign.list(), want)
	}
}

// TestCopyProbeNothingSelected: an app with nothing selected copies
// nothing; the probe gives up at the budget, leaves the clipboard alone and
// leaves no ignored count behind to hide the user's next real copy.
func TestCopyProbeNothingSelected(t *testing.T) {
	pb := &fakePasteboard{snap: PasteboardSnapshot{ChangeCount: 7, Items: userClipboard()}}
	ign := &ignoreLog{}
	start := time.Now()
	got, err := newProbe(pb, func() {}, ign).Probe(context.Background(), 30*time.Millisecond)
	if err != nil || got != "" {
		t.Fatalf("Probe = %q, %v; want nothing", got, err)
	}
	if took := time.Since(start); took < 30*time.Millisecond || took > time.Second {
		t.Fatalf("probe took %v, want about its 30ms budget", took)
	}
	if pb.restoreCount() != 0 || pb.ChangeCount() != 7 || len(ign.list()) != 0 {
		t.Fatalf("an empty probe touched the clipboard: restores %d, cc %d, ignored %v",
			pb.restoreCount(), pb.ChangeCount(), ign.list())
	}
}

// TestCopyProbeIgnoresLineCopies: VS Code copies the line under the caret
// when nothing is selected and says so; that is not a selection.
func TestCopyProbeIgnoresLineCopies(t *testing.T) {
	pb := &fakePasteboard{snap: PasteboardSnapshot{ChangeCount: 3, Items: userClipboard()}}
	got, err := newProbe(pb, func() {
		pb.write(PasteboardItemSnapshot{Representations: []PasteboardRepresentation{
			{Type: "public.utf8-plain-text", Data: []byte("func main() {}\n")},
			{Type: "vscode-editor-data", Data: []byte(`{"version":1,"isFromEmptySelection":true,"multicursorText":null,"mode":"go"}`)},
		}})
	}, &ignoreLog{}).Probe(context.Background(), 200*time.Millisecond)
	if err != nil || got != "" {
		t.Fatalf("a line copy was taken as the selection: %q, %v", got, err)
	}
	if !reflect.DeepEqual(pb.items(), userClipboard()) {
		t.Fatalf("clipboard not restored after a line copy: %+v", pb.items())
	}
	// A real selection in VS Code says isFromEmptySelection:false.
	pb2 := &fakePasteboard{snap: PasteboardSnapshot{ChangeCount: 3, Items: userClipboard()}}
	got, _ = newProbe(pb2, func() {
		pb2.write(PasteboardItemSnapshot{Representations: []PasteboardRepresentation{
			{Type: "public.utf8-plain-text", Data: []byte("selected")},
			{Type: "vscode-editor-data", Data: []byte(`{"version":1,"isFromEmptySelection":false}`)},
		}})
	}, &ignoreLog{}).Probe(context.Background(), 200*time.Millisecond)
	if got != "selected" {
		t.Fatalf("VS Code selection = %q", got)
	}
}

// TestCopyProbeIgnoresFileCopies: ⌘C in Finder copies files, and their
// names come along as text; that is not a text selection.
func TestCopyProbeIgnoresFileCopies(t *testing.T) {
	for _, fileType := range []string{"public.file-url", "15"} {
		pb := &fakePasteboard{snap: PasteboardSnapshot{ChangeCount: 5, Items: userClipboard()}}
		got, _ := newProbe(pb, func() {
			pb.write(PasteboardItemSnapshot{Representations: []PasteboardRepresentation{
				{Type: fileType, Data: []byte("file:///Users/me/IMG_0001.png")},
				{Type: "public.utf8-plain-text", Data: []byte("IMG_0001.png")},
			}})
		}, &ignoreLog{}).Probe(context.Background(), 200*time.Millisecond)
		if got != "" || !reflect.DeepEqual(pb.items(), userClipboard()) {
			t.Fatalf("%s: file copy taken as %q; clipboard %+v", fileType, got, pb.items())
		}
	}
}

// TestCopyProbeUndoesALateCopy: a slow app copies after the probe gave up;
// the clipboard is still put back, and a second writer's content is kept.
func TestCopyProbeUndoesALateCopy(t *testing.T) {
	pb := &fakePasteboard{snap: PasteboardSnapshot{ChangeCount: 10, Items: userClipboard()}}
	ign := &ignoreLog{}
	p := newProbe(pb, func() {
		time.AfterFunc(40*time.Millisecond, func() { pb.write(textItem("late selection")) })
	}, ign)
	p.LateWatch = 500 * time.Millisecond
	if got, _ := p.Probe(context.Background(), 10*time.Millisecond); got != "" {
		t.Fatalf("Probe = %q before the app answered", got)
	}
	waitFor(t, func() bool { return pb.restoreCount() == 1 }, "late restore")
	if !reflect.DeepEqual(pb.items(), userClipboard()) {
		t.Fatalf("late copy not undone: %+v", pb.items())
	}
	if want := []int64{11, 12}; !reflect.DeepEqual(ign.list(), want) {
		t.Fatalf("ignored = %v, want %v", ign.list(), want)
	}

	// Two writes after the probe: someone else copied too, so nothing is
	// restored over their content.
	pb2 := &fakePasteboard{snap: PasteboardSnapshot{ChangeCount: 10, Items: userClipboard()}}
	p2 := newProbe(pb2, func() {
		time.AfterFunc(30*time.Millisecond, func() {
			pb2.write(textItem("late selection"))
			pb2.write(textItem("someone else's copy"))
		})
	}, &ignoreLog{})
	p2.LateWatch = 200 * time.Millisecond
	_, _ = p2.Probe(context.Background(), 5*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	if pb2.restoreCount() != 0 || PasteboardSnapshotText(PasteboardSnapshot{Items: pb2.items()}) != "someone else's copy" {
		t.Fatalf("another writer's content was replaced: %+v", pb2.items())
	}
}

func TestCopyProbeStopsWhenCancelled(t *testing.T) {
	pb := &fakePasteboard{snap: PasteboardSnapshot{ChangeCount: 1, Items: userClipboard()}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	got, _ := newProbe(pb, func() {}, &ignoreLog{}).Probe(ctx, time.Second)
	if got != "" || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("a cancelled probe waited %v (got %q)", time.Since(start), got)
	}
}

func TestPasteboardSnapshotText(t *testing.T) {
	utf16le := func(s string, nul bool) []byte {
		units := utf16.Encode([]rune(s))
		if nul {
			units = append(units, 0)
		}
		b := make([]byte, 0, len(units)*2)
		for _, u := range units {
			b = append(b, byte(u), byte(u>>8))
		}
		return b
	}
	cases := []struct {
		name string
		reps []PasteboardRepresentation
		want string
	}{
		{"macOS text", []PasteboardRepresentation{{Type: "public.png", Data: []byte{1}}, {Type: "public.utf8-plain-text", Data: []byte("选中 text")}}, "选中 text"},
		// CF_TEXT (format 1) is code-page text; only CF_UNICODETEXT is read.
		{"Windows CF_UNICODETEXT", []PasteboardRepresentation{{Type: "1", Data: []byte("ansi")}, {Type: "13", Data: utf16le("中文 👍", true)}}, "中文 👍"},
		{"Windows unicode only", []PasteboardRepresentation{{Type: "13", Data: utf16le("中文 👍", true)}}, "中文 👍"},
		{"no text", []PasteboardRepresentation{{Type: "public.png", Data: []byte{1}}}, ""},
		{"invalid UTF-8", []PasteboardRepresentation{{Type: "public.utf8-plain-text", Data: []byte{'a', 0xff, 'b'}}}, "a�b"},
	}
	for _, tc := range cases {
		got := PasteboardSnapshotText(PasteboardSnapshot{Items: []PasteboardItemSnapshot{{Representations: tc.reps}}})
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
