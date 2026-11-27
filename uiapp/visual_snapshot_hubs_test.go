package uiapp

import (
	"context"
	"errors"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// hubSnapshotNow is the frozen "today" of the hub snapshots. It is the last
// day of every seeded window, so the today column / latest point are stable.
var hubSnapshotNow = time.Date(2026, 11, 20, 15, 4, 0, 0, time.Local)

// agentSettleTimeout bounds the wait for the startup Agent discovery. The wait
// ends the moment discovery settles; the bound only matters under the race
// detector, where that scan is an order of magnitude slower.
const agentSettleTimeout = 45 * time.Second

// hubFixtureAIDays is a realistic 7-day AI usage window, oldest first, ending
// on hubSnapshotNow. One quiet day (zero calls) keeps the zero-height column
// honest; the numbers are deliberately uneven.
func hubFixtureAIDays() []AIUsageDay {
	rows := []struct{ in, out, req int }{
		{18200, 6400, 9},
		{42500, 15800, 21},
		{0, 0, 0},
		{66300, 24900, 33},
		{51700, 19200, 27},
		{38900, 14100, 18},
		{27400, 9800, 12},
	}
	days := make([]AIUsageDay, len(rows))
	for i, r := range rows {
		day := hubSnapshotNow.AddDate(0, 0, i-(len(rows)-1)).Format("2006-01-02")
		days[i] = AIUsageDay{Day: day, Requests: r.req, InputTokens: r.in, OutputTokens: r.out}
	}
	return days
}

// hubFixtureClipboardState is a loaded clipboard snapshot: four kinds with
// uneven sizes and a week of captures.
func hubFixtureClipboardState(filter string) clipboardStatsState {
	counts := []int{12, 31, 18, 44, 27, 9, 23}
	days := make([]storage.ClipboardDay, len(counts))
	for i, n := range counts {
		days[i] = storage.ClipboardDay{Day: hubSnapshotNow.AddDate(0, 0, i-(len(counts)-1)).Format("2006-01-02"), Count: n}
	}
	return clipboardStatsState{
		loaded: true,
		days:   days,
		kinds: []storage.ClipboardKindStat{
			{Kind: "text", Count: 214, Bytes: 1_310_720},
			{Kind: "image", Count: 38, Bytes: 64_424_509},
			{Kind: "file", Count: 12, Bytes: 9_437_184},
			{Kind: "video", Count: 3, Bytes: 31_457_280},
		},
		filter: filter,
	}
}

// hubFixtureLexiconDays is a 14-day hit/miss window with quiet days: those
// days have no events, so the hit-rate line must break there, not drop to 0.
func hubFixtureLexiconDays() []LexiconHitDay {
	rows := []struct{ hits, misses int }{
		{31, 9}, {28, 12}, {0, 0}, {0, 0}, {44, 11}, {52, 10}, {47, 13},
		{0, 0}, {0, 0}, {58, 9}, {63, 8}, {71, 9}, {66, 6}, {49, 4},
	}
	days := make([]LexiconHitDay, len(rows))
	for i, r := range rows {
		days[i] = LexiconHitDay{
			Day:  hubSnapshotNow.AddDate(0, 0, i-(len(rows)-1)).Format("2006-01-02"),
			Hits: r.hits, Misses: r.misses,
		}
	}
	return days
}

func hubFixtureTopWords() []LexiconTopWord {
	return []LexiconTopWord{
		{Word: "idempotent", UseCount: 41},
		{Word: "幂等", UseCount: 33},
		{Word: "kubernetes", UseCount: 27},
		{Word: "回滚", UseCount: 19},
		{Word: "SLO", UseCount: 8},
	}
}

func hubFixtureAgentSessions() []agentsession.Session {
	mk := func(id, title string, status agentsession.Status, review bool, ago time.Duration) agentsession.Session {
		return agentsession.Session{
			ID: id, Provider: "codex", Title: title, Status: status, PendingReview: review,
			LastActivityAt: hubSnapshotNow.Add(-ago),
		}
	}
	return []agentsession.Session{
		mk("r1", "GPU layout pass", agentsession.StatusRunning, false, time.Minute),
		mk("r2", "IME candidate window", agentsession.StatusRunning, false, 3*time.Minute),
		mk("a1", "SQLite index review", agentsession.StatusWaiting, true, 6*time.Minute),
		mk("d1", "Clipboard audit", agentsession.StatusStopped, false, 42*time.Minute),
		mk("d2", "Release notes", agentsession.StatusStopped, false, 2*time.Hour),
		mk("d3", "Docs refresh", agentsession.StatusStopped, false, 5*time.Hour),
		mk("d4", "Lint sweep", agentsession.StatusStopped, false, 26*time.Hour),
	}
}

// hubSeedLoaded injects fully loaded snapshots through the controller's own
// cache seams, exactly where the async readers would have put them.
func hubSeedLoaded(ctrl *AppController, clipFilter string) {
	ctrl.aiUsageMu.Lock()
	ctrl.aiUsageDays, ctrl.aiUsageErr, ctrl.aiUsageLoaded, ctrl.aiUsageRefreshStarted = hubFixtureAIDays(), nil, true, false
	ctrl.aiUsageMu.Unlock()

	ctrl.clipboardStatsMu.Lock()
	ctrl.clipboardStats = hubFixtureClipboardState(clipFilter)
	ctrl.clipboardStatsMu.Unlock()

	ctrl.lexiconHitsMu.Lock()
	ctrl.lexiconHitDays, ctrl.lexiconHitsErr, ctrl.lexiconHitsLoaded, ctrl.lexiconHitsRefreshStarted = hubFixtureLexiconDays(), nil, true, false
	ctrl.lexiconHitsMu.Unlock()

	ctrl.lexiconTopWordsMu.Lock()
	ctrl.lexiconTopWords, ctrl.lexiconTopWordsErr, ctrl.lexiconTopWordsLoaded, ctrl.lexiconTopWordsRefreshing = hubFixtureTopWords(), nil, true, false
	ctrl.lexiconTopWordsMu.Unlock()

	ctrl.agentMu.Lock()
	ctrl.agentSessions, ctrl.agentDiagnostics = hubFixtureAgentSessions(), nil
	setAgentOperationResult(ctrl, host.OperationResult{State: host.StateReady, ObservedAt: hubSnapshotNow})
	ctrl.agentMu.Unlock()
}

// hubSeedStates puts every Stats card into a non-happy state at once:
// AI loading, clipboard failed, lexicon with no events at all, and no agent
// sessions. It proves the loading / error / empty branches survive the charts.
func hubSeedStates(ctrl *AppController) {
	ctrl.aiUsageMu.Lock()
	ctrl.aiUsageDays, ctrl.aiUsageErr, ctrl.aiUsageLoaded, ctrl.aiUsageRefreshStarted = nil, nil, false, true
	ctrl.aiUsageMu.Unlock()

	ctrl.clipboardStatsMu.Lock()
	ctrl.clipboardStats = clipboardStatsState{loaded: true, err: errors.New("db locked")}
	ctrl.clipboardStatsMu.Unlock()

	ctrl.lexiconHitsMu.Lock()
	quiet := hubFixtureLexiconDays()
	for i := range quiet {
		quiet[i].Hits, quiet[i].Misses = 0, 0
	}
	ctrl.lexiconHitDays, ctrl.lexiconHitsErr, ctrl.lexiconHitsLoaded, ctrl.lexiconHitsRefreshStarted = quiet, nil, true, false
	ctrl.lexiconHitsMu.Unlock()

	ctrl.lexiconTopWordsMu.Lock()
	ctrl.lexiconTopWords, ctrl.lexiconTopWordsErr, ctrl.lexiconTopWordsLoaded, ctrl.lexiconTopWordsRefreshing = nil, nil, true, false
	ctrl.lexiconTopWordsMu.Unlock()

	ctrl.agentMu.Lock()
	ctrl.agentSessions, ctrl.agentDiagnostics = nil, nil
	setAgentOperationResult(ctrl, host.OperationResult{State: host.StateReady, ObservedAt: hubSnapshotNow})
	ctrl.agentMu.Unlock()
}

// hubSnapshotShoot renders view at the capsule size, lets animations settle
// and writes a PNG. drive may move the pointer or press keys first.
func hubSnapshotShoot(t *testing.T, dir, name string, view func(c *ui.Context), w, h int, dark bool, drive func(tt *ui.Tester)) {
	t.Helper()
	tt := ui.NewTester(view, w, h)
	tt.SetDark(dark)
	tt.SetScale(2)
	tt.Frame()
	if drive != nil {
		drive(tt)
	}
	tt.Advance(2 * time.Second)
	tt.Frame()
	out, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

// TestVisualSnapshotsHubs renders the three Capsule hubs (Stats ⌘7, Manage
// ⌘8, Assets ⌘9) for zh/en × light/dark at the real Omnibox size. It is
// opt-in, like TestVisualSnapshots:
//
//	CLAVIS_SNAPSHOT_DIR=/tmp/clavis-polish/hubs go test -count=1 -run TestVisualSnapshotsHubs ./uiapp/
//
// Every number on the Stats hub comes from the seeded async snapshots above;
// nothing is drawn from a literal in the view.
func TestVisualSnapshotsHubs(t *testing.T) {
	dir := os.Getenv("CLAVIS_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set CLAVIS_SNAPSHOT_DIR to write hub snapshots")
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
			suffix := l.tag + "-" + mode
			t.Run(suffix, func(t *testing.T) {
				defer i18n.SetLanguage(i18n.English)
				i18n.SetLanguage(l.lang)
				renderHubSnapshots(t, dir, suffix, l.lang, dark)
			})
		}
	}
	// Every frame must be a real render, never an empty or flat surface.
	validateVisualSnapshotPNGs(t, dir)
}

func renderHubSnapshots(t *testing.T, dir, suffix string, lang i18n.Language, dark bool) {
	doer := mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse("ok"), nil
	}}
	f := newInteractionFixture(t, doer)
	restoreClock := clipboardClock
	clipboardClock = func() time.Time { return hubSnapshotNow }
	defer func() { clipboardClock = restoreClock }()

	// Pin the language the way saving the config would; otherwise every "zh"
	// frame silently renders English.
	if err := f.ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Language = string(lang) }); err != nil {
		t.Fatal(err)
	}
	appearance := f.ctrl.Config().Main.Appearance

	// Let the startup Agent discovery settle before seeding, so a background
	// refresh cannot overwrite the deterministic sessions mid-render.
	waitUntil(t, agentSettleTimeout, func() bool {
		r := f.ctrl.agentRecordedOperationResult()
		return !f.ctrl.agentRefreshing() && r.State != "" && r.State != host.StateRefreshing
	}, "startup agent refresh settle")

	mediaPath := filepath.Join(t.TempDir(), "preview.png")
	if err := os.WriteFile(mediaPath, testPNG16(t), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, clip := range []clipboard.CaptureCandidate{
		{Content: "preview-media-deck", SourceAppName: "Photos", Timestamp: hubSnapshotNow.Add(-26 * time.Hour),
			Media: clipboard.Media{Kind: clipboard.KindImage, BlobSHA: "deck-sha", MediaPath: mediaPath, ThumbPath: mediaPath, Width: 1920, Height: 1080, ByteSize: 2400000}},
		{Content: "https://github.com/yeisme/clavis/pull/12", SourceAppName: "Safari", Timestamp: hubSnapshotNow.Add(-3 * time.Hour)},
	} {
		if _, _, err := f.clips.Record(context.Background(), clip); err != nil {
			t.Fatal(err)
		}
	}

	ctx := &engine.CapsuleContext{AppName: "Visual Studio Code"}
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)

	hubSeedLoaded(f.ctrl, "")
	f.ctrl.SetHubSurface(HubSurfaceStats)
	hubSnapshotShoot(t, dir, "30-hub-stats-"+suffix, f.ctrl.CapsuleView, w, h, dark, nil)

	// Pointer over the AI chart's today column: the selection rule and tooltip.
	hubSnapshotShoot(t, dir, "30b-hub-stats-hover-"+suffix, f.ctrl.CapsuleView, w, h, dark, func(tt *ui.Tester) {
		if r, ok := tt.Find("stats-ai-chart-area"); ok {
			tt.Move(r.X+r.W*0.62, r.Y+r.H*0.45)
			tt.Frame()
		}
	})
	// The lexicon line (a gap day) and the captures columns, each with its tooltip.
	hubSnapshotShoot(t, dir, "30e-hub-stats-hover-lexicon-"+suffix, f.ctrl.CapsuleView, w, h, dark, func(tt *ui.Tester) {
		if r, ok := tt.Find("stats-lex-chart-area"); ok {
			tt.Move(r.X+r.W*0.88, r.Y+r.H*0.40)
			tt.Frame()
		}
	})
	hubSnapshotShoot(t, dir, "30f-hub-stats-hover-clipboard-"+suffix, f.ctrl.CapsuleView, w, h, dark, func(tt *ui.Tester) {
		if r, ok := tt.Find("stats-clip-chart-area"); ok {
			tt.Move(r.X+r.W*0.60, r.Y+r.H*0.50)
			tt.Frame()
		}
	})

	// A clipboard kind filter active in the Clipboard tab highlights its segment.
	hubSeedLoaded(f.ctrl, "image")
	hubSnapshotShoot(t, dir, "30c-hub-stats-filter-"+suffix, f.ctrl.CapsuleView, w, h, dark, nil)

	// Loading / error / empty states of the four cards.
	hubSeedStates(f.ctrl)
	hubSnapshotShoot(t, dir, "30d-hub-stats-states-"+suffix, f.ctrl.CapsuleView, w, h, dark, nil)

	// The Manage hub with loaded stats, so its media card draws its quota
	// meters; a 500-row history cap keeps the fixture's 267 rows under it.
	hubSeedLoaded(f.ctrl, "")
	_ = f.ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Clipboard.MaxHistoryItems = 500 })
	f.ctrl.SetHubSurface(HubSurfaceManage)
	hubSnapshotShoot(t, dir, "31-hub-manage-"+suffix, f.ctrl.CapsuleView, w, h, dark, nil)
}
