package uiapp

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestSelectionBarViewClickAndShortcutDispatchAndEscape(t *testing.T) {
	actions := []string{"optimize_prompt", "explain", "translate", "polish", "copy"}
	payload := SelectionBarPayload{
		Text:    "hello selection bar",
		Actions: actions,
		Anchor:  host.CursorAnchor{X: 100, Y: 100, Valid: true},
	}
	appearance := config.DefaultAppearanceConfig()
	w, h := SelectionBarSize(payload)

	t.Run("clicking every action pill dispatches action and hides bar", func(t *testing.T) {
		for _, wantAction := range actions {
			var mu sync.Mutex
			var copied []string
			hideCalls := 0
			b := &inplaceTestBridge{
				mockBridge: newMockBridge(),
				field: host.EditableFieldSnapshot{
					Value:          "hello selection bar",
					SelectedText:   "hello selection bar",
					SelectionStart: 0,
					SelectionEnd:   len([]rune("hello selection bar")),
				},
			}
			ctrl := newInplaceTestController(t, b, Options{
				ClipboardWriter: func(s string) {
					mu.Lock()
					copied = append(copied, s)
					mu.Unlock()
				},
				OnHideSelectionBar: func() {
					mu.Lock()
					hideCalls++
					mu.Unlock()
				},
			})

			tt := ui.NewTester(func(c *ui.Context) {
				SelectionBarViewWithController(c, payload, appearance, ctrl)
			}, w, h)
			tt.Frame()
			if err := tt.Click(SelectionBarActionLabel(wantAction)); err != nil {
				t.Fatalf("click %q (%q) failed: %v (texts=%v)", wantAction, SelectionBarActionLabel(wantAction), err, tt.Texts())
			}
			tt.Frame()

			mu.Lock()
			gotHides := hideCalls
			gotCopied := append([]string(nil), copied...)
			mu.Unlock()
			if gotHides != 1 {
				t.Fatalf("action %q hideCalls = %d, want 1", wantAction, gotHides)
			}
			if wantAction == "copy" {
				if len(gotCopied) != 1 || gotCopied[0] != payload.Text {
					t.Fatalf("copy action wrote %v, want [%q]", gotCopied, payload.Text)
				}
			} else {
				wantMode := wantAction
				if wantMode == "optimize_prompt" {
					wantMode = "optimize"
				}
				ctrl.mu.Lock()
				tr := ctrl.transform
				ctrl.mu.Unlock()
				if tr == nil || tr.mode != wantMode {
					t.Fatalf("action %q transform = %+v, want mode %q", wantAction, tr, wantMode)
				}
			}
		}
	})

	t.Run("digit keys 1-5 trigger actions and Esc hides bar", func(t *testing.T) {
		keys := []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5}
		for i, key := range keys {
			wantAction := actions[i]
			var mu sync.Mutex
			var copied []string
			hideCalls := 0
			b := &inplaceTestBridge{
				mockBridge: newMockBridge(),
				field: host.EditableFieldSnapshot{
					Value:          "hello selection bar",
					SelectedText:   "hello selection bar",
					SelectionStart: 0,
					SelectionEnd:   len([]rune("hello selection bar")),
				},
			}
			ctrl := newInplaceTestController(t, b, Options{
				ClipboardWriter: func(s string) {
					mu.Lock()
					copied = append(copied, s)
					mu.Unlock()
				},
				OnHideSelectionBar: func() {
					mu.Lock()
					hideCalls++
					mu.Unlock()
				},
			})

			tt := ui.NewTester(func(c *ui.Context) {
				SelectionBarViewWithController(c, payload, appearance, ctrl)
			}, w, h)
			tt.Frame()
			tt.Key(0, key)
			tt.Frame()

			mu.Lock()
			gotHides := hideCalls
			gotCopied := append([]string(nil), copied...)
			mu.Unlock()
			if gotHides != 1 {
				t.Fatalf("key %d (%q) hideCalls = %d, want 1", i+1, wantAction, gotHides)
			}
			if wantAction == "copy" {
				if len(gotCopied) != 1 || gotCopied[0] != payload.Text {
					t.Fatalf("key 5 copy wrote %v, want [%q]", gotCopied, payload.Text)
				}
			} else {
				wantMode := wantAction
				if wantMode == "optimize_prompt" {
					wantMode = "optimize"
				}
				ctrl.mu.Lock()
				tr := ctrl.transform
				ctrl.mu.Unlock()
				if tr == nil || tr.mode != wantMode {
					t.Fatalf("key %d (%q) transform = %+v, want mode %q", i+1, wantAction, tr, wantMode)
				}
			}
		}

		// Esc hides the selection bar without starting any transform or copying.
		var mu sync.Mutex
		hideCalls := 0
		copied := 0
		b := &inplaceTestBridge{mockBridge: newMockBridge()}
		ctrl := newInplaceTestController(t, b, Options{
			ClipboardWriter: func(string) {
				mu.Lock()
				copied++
				mu.Unlock()
			},
			OnHideSelectionBar: func() {
				mu.Lock()
				hideCalls++
				mu.Unlock()
			},
		})
		tt := ui.NewTester(func(c *ui.Context) {
			SelectionBarViewWithController(c, payload, appearance, ctrl)
		}, w, h)
		tt.Frame()
		tt.Key(0, ui.KeyEscape)
		tt.Frame()

		mu.Lock()
		gotHides := hideCalls
		gotCopied := copied
		mu.Unlock()
		if gotHides != 1 || gotCopied != 0 {
			t.Fatalf("Esc hideCalls=%d copied=%d, want 1 and 0", gotHides, gotCopied)
		}
		ctrl.mu.Lock()
		tr := ctrl.transform
		ctrl.mu.Unlock()
		if tr != nil {
			t.Fatalf("Esc unexpectedly started transform: %+v", tr)
		}
	})
}
