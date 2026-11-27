package uiapp

import (
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// codeSnapAIExtraHeight is how much taller than the preferred AI window the
// AI answer frame is, in DIPs.
const codeSnapAIExtraHeight = 200

// Code samples the syntax snapshots draw. They are plain strings with explicit
// tabs so the indentation the renderer shows is the indentation written here.
const (
	codeSnapSQL = "SELECT id, content, kind\nFROM clipboard_items\nWHERE kind = 'image'\nORDER BY updated_at DESC\nLIMIT 20;"

	codeSnapShell = `git commit -m "feat: highlight code previews" && git push origin main`

	codeSnapGo = "// Retry calls fn up to n times with exponential backoff.\n" +
		"func Retry(n int, fn func() error) error {\n" +
		"\tdelay := 100 * time.Millisecond\n" +
		"\tfor i := 0; i < n; i++ {\n" +
		"\t\tif err := fn(); err == nil {\n" +
		"\t\t\treturn nil\n" +
		"\t\t}\n" +
		"\t\ttime.Sleep(delay)\n" +
		"\t\tdelay *= 2\n" +
		"\t}\n" +
		"\treturn errors.New(\"retry: giving up\")\n" +
		"}"

	codeSnapJSON = `{"name": "clavis", "version": "1.4.0", "private": true, "keywords": ["launcher", "clipboard"], "engines": {"go": ">=1.22"}}`

	// A YAML mapping without list items: a "- key: value" list is read as a
	// Markdown bullet list by the preview classifier before it reaches code.
	codeSnapYAML = "server:\n" +
		"  host: 0.0.0.0\n" +
		"  port: 8080\n" +
		"  tls:\n" +
		"    enabled: true\n" +
		"    cert: /etc/clavis/tls.crt\n" +
		"database:\n" +
		"  name: clavis # local dev\n" +
		"  pool: 10\n" +
		"  timeout: \"5s\"\n"

	// codeSnapAnswerEN and codeSnapAnswerZH are the AI answers the mocked model
	// streams: prose, a fenced Go block and prose again.
	codeSnapAnswerEN = "This loads the config and wraps every failure with the path that caused it:\n\n" +
		"```go\n" +
		"// LoadConfig reads and decodes the JSON config at path.\n" +
		"func LoadConfig(path string) (*Config, error) {\n" +
		"\tdata, err := os.ReadFile(path)\n" +
		"\tif err != nil {\n" +
		"\t\treturn nil, fmt.Errorf(\"read %s: %w\", path, err)\n" +
		"\t}\n" +
		"\tvar cfg Config\n" +
		"\tif err := json.Unmarshal(data, &cfg); err != nil {\n" +
		"\t\treturn nil, err\n" +
		"\t}\n" +
		"\treturn &cfg, nil\n" +
		"}\n" +
		"```\n\n" +
		"Call it once at startup and keep the result."
	codeSnapAnswerZH = "这段代码读取并解码 JSON 配置文件，每个错误都会带上出错的路径：\n\n" +
		"```go\n" +
		"// LoadConfig 读取并解码 path 处的 JSON 配置。\n" +
		"func LoadConfig(path string) (*Config, error) {\n" +
		"\tdata, err := os.ReadFile(path)\n" +
		"\tif err != nil {\n" +
		"\t\treturn nil, fmt.Errorf(\"read %s: %w\", path, err)\n" +
		"\t}\n" +
		"\tvar cfg Config\n" +
		"\tif err := json.Unmarshal(data, &cfg); err != nil {\n" +
		"\t\treturn nil, err\n" +
		"\t}\n" +
		"\treturn &cfg, nil\n" +
		"}\n" +
		"```\n\n" +
		"启动时调用一次并缓存结果即可。"
)

// jsonStreamChunk escapes text as the inside of a JSON string, which is what
// sseResponse expects of a chunk.
func jsonStreamChunk(t *testing.T, text string) string {
	t.Helper()
	b, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	return string(b[1 : len(b)-1])
}

// codeStreamChunks splits an answer into the chunks a model streams, a few
// lines at a time, each already escaped for sseResponse.
func codeStreamChunks(t *testing.T, answer string) []string {
	t.Helper()
	var chunks []string
	lines := strings.SplitAfter(answer, "\n")
	for i := 0; i < len(lines); i += 4 {
		chunks = append(chunks, jsonStreamChunk(t, strings.Join(lines[i:min(i+4, len(lines))], "")))
	}
	return chunks
}

// TestVisualSnapshotsCode renders every surface that shows code (the clipboard
// preview for SQL, Go and a shell command, and an AI answer with a fenced Go
// block) in both languages and both appearances, so the syntax colors can be
// reviewed against Xcode's. It is opt-in like TestVisualSnapshots:
//
//	CLAVIS_SNAPSHOT_DIR=/tmp/clavis-polish/code go test -count=1 -run TestVisualSnapshotsCode ./uiapp/
func TestVisualSnapshotsCode(t *testing.T) {
	dir := os.Getenv("CLAVIS_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set CLAVIS_SNAPSHOT_DIR to write visual snapshots")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	langs := []struct {
		tag  string
		lang i18n.Language
	}{{"zh", i18n.Chinese}, {"en", i18n.English}}

	for _, l := range langs {
		for _, dark := range []bool{false, true} {
			mode := "light"
			if dark {
				mode = "dark"
			}
			t.Run(l.tag+"-"+mode, func(t *testing.T) {
				defer i18n.SetLanguage(i18n.English)
				i18n.SetLanguage(l.lang)
				renderCodeSnapshots(t, dir, l.tag, mode, l.lang, dark)
			})
		}
	}
	validateVisualSnapshotPNGs(t, dir)
}

func renderCodeSnapshots(t *testing.T, dir, tag, mode string, lang i18n.Language, dark bool) {
	t.Helper()
	suffix := tag + "-" + mode
	// CLAVIS_SNAPSHOT_THEME=macos draws the frames in the macOS grayscale
	// palette (macOSTestTheme) instead of MyGo's zinc defaults.
	macOS := os.Getenv("CLAVIS_SNAPSHOT_THEME") == "macos"
	shoot := func(name string, view func(c *ui.Context), w, h int, drive func(tt *ui.Tester)) {
		t.Helper()
		frame := view
		if macOS {
			frame = func(c *ui.Context) {
				c.SetTheme(macOSTestTheme(dark).WithPreferences(c.Preferences()))
				view(c)
			}
		}
		tt := ui.NewTester(frame, w, h)
		tt.SetDark(dark)
		tt.SetScale(2)
		tt.Frame()
		if drive != nil {
			drive(tt)
		}
		tt.Advance(2 * time.Second)
		tt.Frame()
		out, err := os.Create(filepath.Join(dir, name+"-"+suffix+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		if err := png.Encode(out, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	// The frames use the classic preset, whose capsule body is vibrancy; the
	// glass and spatial presets draw the same wells over their own tint:
	//
	//	CLAVIS_SNAPSHOT_PRESET=liquid_glass CLAVIS_SNAPSHOT_DIR=... go test ...
	preset := "classic"
	if p := os.Getenv("CLAVIS_SNAPSHOT_PRESET"); p != "" {
		preset = p
	}
	pin := func(ctrl *AppController, motion string) {
		_ = ctrl.UpdateMainConfig(func(m *config.MainConfig) {
			m.Appearance.VisualPreset = preset
			m.Appearance.Animations = motion
			// Saving the config re-resolves [appearance].language; pin it to
			// the subtest's language or every "zh" frame renders English.
			m.Appearance.Language = string(lang)
		})
	}

	// Clipboard preview: the newest entries first, so the SQL row leads.
	f := newInteractionFixture(t, nil)
	snapNow := time.Date(2026, 11, 20, 15, 4, 0, 0, time.Local)
	restoreClock := clipboardClock
	clipboardClock = func() time.Time { return snapNow }
	defer func() { clipboardClock = restoreClock }()
	for _, clip := range []clipboard.CaptureCandidate{
		{Content: "https://github.com/yeisme/clavis/pull/12", SourceAppName: "Safari", Timestamp: snapNow.AddDate(0, 0, -4)},
		{Content: codeSnapYAML, SourceAppName: "Xcode", Timestamp: snapNow.Add(-5 * time.Hour)},
		{Content: codeSnapJSON, SourceAppName: "Terminal", Timestamp: snapNow.Add(-4 * time.Hour)},
		{Content: codeSnapShell, SourceAppName: "Terminal", Timestamp: snapNow.Add(-3 * time.Hour)},
		{Content: codeSnapGo, SourceAppName: "Xcode", Timestamp: snapNow.Add(-90 * time.Minute)},
		{Content: "微信支付回调签名校验失败，请检查商户证书与 APIv3 密钥是否匹配，然后重新发起退款请求。", Timestamp: snapNow.Add(-18 * time.Minute)},
		{Content: codeSnapSQL, SourceAppName: "Notes", Timestamp: snapNow.Add(-20 * time.Second)},
	} {
		if _, _, err := f.clips.Record(context.Background(), clip); err != nil {
			t.Fatal(err)
		}
	}
	pin(f.ctrl, "reduced")
	appearance := f.ctrl.Config().Main.Appearance

	selectClip := func(prefix string) {
		t.Helper()
		f.ctrl.OpenOmnibox(config.TabClipboard, "")
		waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 7 }, "clipboard items")
		for i, item := range f.ctrl.State().OmniboxItems {
			if strings.HasPrefix(item.ClipboardContent, prefix) {
				f.ctrl.SelectIndex(i)
				return
			}
		}
		t.Fatalf("clipboard item %q not found", prefix)
	}
	shootClip := func(name string) {
		t.Helper()
		w, h := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
		shoot(name, f.ctrl.CapsuleView, w, h, nil)
	}

	selectClip("SELECT id")
	shootClip("40-code-sql")
	selectClip("// Retry calls")
	shootClip("41-code-go")
	selectClip("git commit")
	shootClip("43-code-shell")
	selectClip("{\"name\"")
	shootClip("44-code-json")
	selectClip("server:")
	shootClip("45-code-yaml")

	// The raw toggle draws the same text in the plain monospace well.
	selectClip("SELECT id")
	capsuleUI.toggleRawPreview(f.ctrl)
	shootClip("46-code-sql-raw")
	capsuleUI.toggleRawPreview(f.ctrl)

	// A code snippet: its template and its rendered text draw through the same
	// preview lines, inside the snippet's own inset well. Motion is off for this
	// frame: landing on a snippet flashes its preview card, which leaves an
	// opaque theme.Background fill on the card that is not what this frame is
	// about (it shows as a darker pane in dark mode).
	pin(f.ctrl, "off")
	cfg := f.ctrl.Config()
	cfg.Snippets.Items = append(cfg.Snippets.Items, config.SnippetItem{
		ID: "snap-gotest", Trigger: ";gotest", Description: "Go test skeleton", WordBoundary: true,
		Content: "func TestFoo(t *testing.T) {\n\tgot := Foo(42)\n\tif got != \"ok\" {\n\t\tt.Errorf(\"Foo() = %q\", got)\n\t}\n}",
	})
	if err := f.ctrl.ConfigStore.SaveSnippets(cfg.Snippets); err != nil {
		t.Fatalf("SaveSnippets: %v", err)
	}
	f.ctrl.SetConfig(f.ctrl.ConfigStore.Current())
	f.ctrl.OpenOmnibox(config.TabSnippets, "gotest")
	shootClip("47-code-snippet")

	// AI answer: a mocked model streams prose around a fenced Go block.
	answer := codeSnapAnswerEN
	prompt := "?Explain this function"
	if lang == i18n.Chinese {
		answer = codeSnapAnswerZH
		prompt = "?解释这个函数"
	}
	chunks := codeStreamChunks(t, answer)
	af := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse(chunks...), nil
	}})
	pin(af.ctrl, "reduced")
	// No selected text: with one the model's reply is a rewrite to approve, and
	// this frame is a plain question and answer.
	af.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: &engine.CapsuleContext{
		AppName: "Xcode",
	}})
	w, h := PreferredCapsuleSizeForState(af.ctrl.State(), appearance)
	driver := ui.NewTester(af.ctrl.CapsuleView, w, h)
	driver.Frame()
	driver.Type(prompt)
	driver.Frame()
	driver.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := af.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI completion")
	w, h = PreferredCapsuleSizeForState(af.ctrl.State(), appearance)
	// The answer's code block is taller than the answer viewport of the default
	// window, which scrolls: the frame is made taller so every line of the block
	// (and so every token color) is in view.
	shoot("42-code-ai", af.ctrl.CapsuleView, w, h+codeSnapAIExtraHeight, nil)
}
