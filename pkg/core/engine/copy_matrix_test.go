package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestCopyMatrixFormattingAndMediaPaths(t *testing.T) {
	if got := formatMarkdownFence("hello\r\n\n"); got != "```\nhello\n```" {
		t.Fatalf("formatMarkdownFence plain = %q", got)
	}
	nested := "before\n```go\nfmt.Println(1)\n```\nafter\r\n"
	fenced := formatMarkdownFence(nested)
	if !strings.HasPrefix(fenced, "````\n") || !strings.HasSuffix(fenced, "\n````") {
		t.Fatalf("expected 4-backtick outer fence for nested triple-backticks, got %q", fenced)
	}

	multi := ClipboardMedia{
		Kind:       "image",
		Path:       " /tmp/fallback.png ",
		FilePaths:  []string{"/tmp/a.png", "/tmp/b.png"},
		Mime:       "image/png",
		Width:      1920,
		Height:     1080,
		ByteSize:   4096,
		DurationMs: 2500,
	}
	if got := mediaPathsCopyText(multi); got != "/tmp/a.png\n/tmp/b.png" {
		t.Fatalf("mediaPathsCopyText multi = %q", got)
	}
	if got := mediaPrimaryPath(multi); got != "/tmp/a.png" {
		t.Fatalf("mediaPrimaryPath multi = %q", got)
	}

	blankFirst := ClipboardMedia{Path: " /tmp/only.png ", FilePaths: []string{"   "}}
	if got := mediaPrimaryPath(blankFirst); got != "/tmp/only.png" {
		t.Fatalf("mediaPrimaryPath blank FilePaths[0] fallback = %q", got)
	}
	if got := mediaPathsCopyText(ClipboardMedia{Path: " /tmp/single.png "}); got != "/tmp/single.png" {
		t.Fatalf("mediaPathsCopyText single = %q", got)
	}

	meta := formatMediaMetadataSafe(OmniboxItem{KeyHint: KeyHintPinned}, multi)
	for _, want := range []string{
		"kind: image",
		"mime: image/png",
		"dimensions: 1920x1080",
		"bytes: 4096",
		"duration_ms: 2500",
		"path: /tmp/a.png",
		"pinned: true",
	} {
		if !strings.Contains(meta, want) {
			t.Fatalf("formatMediaMetadataSafe missing %q in:\n%s", want, meta)
		}
	}

	minimal := formatMediaMetadataSafe(OmniboxItem{}, ClipboardMedia{Kind: "audio"})
	if minimal != "kind: audio\npinned: false" {
		t.Fatalf("minimal metadata = %q", minimal)
	}

	captureActions := makeCaptureActionItems()
	if len(captureActions) != 2 || captureActions[0].LeaderAction == nil || captureActions[1].LeaderAction == nil {
		t.Fatalf("expected 2 capture actions with LeaderAction, got %+v", captureActions)
	}
	if captureActions[0].ID != "action:clavis-capture:annotate" || captureActions[1].ID != "action:clavis-capture:ocr" {
		t.Fatalf("unexpected capture action IDs: %+v", captureActions)
	}
}

func TestCopyMatrixOmniboxActionsAndEffects(t *testing.T) {
	sm := NewStateMachine(config.DefaultConfig())
	hasAction := func(specs []OmniboxActionSpec, id string) bool {
		for _, s := range specs {
			if s.ID == id {
				return true
			}
		}
		return false
	}

	// Media clipboard entry: image has ocr; non-image (file/audio) omits ocr.
	imgSpecs := AvailableItemActions(OmniboxItem{
		Kind:           ItemClipboardEntry,
		ClipboardMedia: &ClipboardMedia{Kind: "image", Path: "/tmp/pic.png"},
	})
	for _, want := range []string{"copy_path", "reveal", "copy_meta", "ocr"} {
		if !hasAction(imgSpecs, want) {
			t.Fatalf("image clipboard specs missing %q: %+v", want, imgSpecs)
		}
	}
	if hasAction(imgSpecs, "copy_md") || hasAction(imgSpecs, "translate") {
		t.Fatalf("image clipboard specs should not expose text actions: %+v", imgSpecs)
	}

	fileSpecs := AvailableItemActions(OmniboxItem{
		Kind:           ItemClipboardEntry,
		ClipboardMedia: &ClipboardMedia{Kind: "file", Path: "/tmp/doc.pdf"},
	})
	if hasAction(fileSpecs, "ocr") {
		t.Fatalf("non-image media should not expose ocr: %+v", fileSpecs)
	}

	// Sensitive utility hides copy_md in specs and rejects EventExecuteItemAction{ActionID: "copy_md"}.
	sensitiveSpecs := AvailableItemActions(OmniboxItem{Kind: ItemUtility, UtilitySensitive: true, UtilityValue: "secret-pw"})
	if hasAction(sensitiveSpecs, "copy_md") {
		t.Fatalf("sensitive utility leaked copy_md action: %+v", sensitiveSpecs)
	}
	_ = sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions, Query: "pw 16"})
	if effs := sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "copy_md"}); len(effs) == 0 || effs[0].Type != EffectCopyText || !effs[0].Sensitive || strings.Contains(effs[0].Text, "```") {
		t.Fatalf("sensitive utility copy_md must preserve Sensitive TTL copy without markdown fence, got %+v", effs)
	}

	// Non-sensitive utility wraps value cleanly with copy_md.
	_ = sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions, Query: "sha256 clavis"})
	effs := sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "copy_md"})
	if len(effs) != 2 || effs[0].Type != EffectCopyText || !strings.HasPrefix(effs[0].Text, "```\n") {
		t.Fatalf("expected fenced EffectCopyText on utility copy_md, got %+v", effs)
	}

	// Media clipboard missing paths fail safely (no empty copy_path / reveal effects).
	_ = sm.Handle(Event{
		Type: EventSetClipboardEntries,
		ClipboardEntries: []OmniboxClipboardEntry{{
			ID:        "empty-media",
			Content:   "broken",
			CopiedAt:  time.Now(),
			Media:     ClipboardMedia{Kind: "image"},
		}},
	})
	_ = sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabClipboard})
	if effs := sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "copy_path"}); len(effs) != 0 {
		t.Fatalf("empty media path copy_path should emit no effect, got %+v", effs)
	}
	if effs := sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "reveal"}); len(effs) != 0 {
		t.Fatalf("empty media path reveal should emit no effect, got %+v", effs)
	}

	// Image OCR action and text translate fallback (when ClipboardContent is blank) trigger EffectStartAI.
	_ = sm.Handle(Event{
		Type: EventSetClipboardEntries,
		ClipboardEntries: []OmniboxClipboardEntry{{
			ID:       "img-ocr-1",
			Content:  "img.png",
			CopiedAt: time.Now(),
			Media:    ClipboardMedia{Kind: "image", Path: "/tmp/img.png", Mime: "image/png"},
		}},
	})
	_ = sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabClipboard})
	effs = sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "ocr"})
	if len(effs) != 2 || effs[1].Type != EffectStartAI || effs[1].Mode != "ocr" || effs[1].ClipboardMedia == nil {
		t.Fatalf("expected EffectStartAI ocr with ClipboardMedia, got %+v", effs)
	}

	_ = sm.Handle(Event{
		Type: EventSetClipboardEntries,
		ClipboardEntries: []OmniboxClipboardEntry{{
			ID:       "txt-tr-1",
			Content:  "需要翻译的中文段落",
			CopiedAt: time.Now(),
		}},
	})
	_ = sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabClipboard})
	effs = sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "translate"})
	if len(effs) != 2 || effs[1].Type != EffectStartAI || effs[1].Mode != "translate" || effs[1].SourceText != "需要翻译的中文段落" {
		t.Fatalf("expected EffectStartAI translate, got %+v", effs)
	}
}
