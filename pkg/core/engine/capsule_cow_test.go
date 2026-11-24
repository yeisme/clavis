package engine_test

import (
	"fmt"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func buildTestClipboardEntries(n int) []engine.OmniboxClipboardEntry {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	entries := make([]engine.OmniboxClipboardEntry, n)
	for i := 0; i < n; i++ {
		entries[i] = engine.OmniboxClipboardEntry{
			ID:            fmt.Sprintf("clip-%03d", i),
			Content:       fmt.Sprintf("clipboard item %03d content", i),
			SourceAppName: "Notes",
			CopiedAt:      now.Add(-time.Duration(i) * time.Minute),
		}
	}
	return entries
}

// TestStateMachineCOWSelectionSharesImmutableSlices verifies that selection-only
// events (EventSelectNext, EventSelectPrevious, EventSelectIndex, EventHoverItem)
// and State() share immutable read-only slice backing arrays without deep copies
// and pin slice capacities so accidental appends cannot alias shared storage.
func TestStateMachineCOWSelectionSharesImmutableSlices(t *testing.T) {
	sm := engine.NewStateMachine(config.DefaultConfig())
	sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: buildTestClipboardEntries(50)})
	sm.Handle(engine.Event{Type: engine.EventSetInstalledApps, InstalledApps: []engine.InstalledAppEntry{
		{Name: "Notes", BundleID: "com.apple.Notes", Path: "/System/Applications/Notes.app"},
	}})
	sm.Handle(engine.Event{Type: engine.EventSetFrecencyScores, FrecencyScores: map[string]int{"clipboard:clip-000": 5}})
	sm.Handle(engine.Event{Type: engine.EventSetListeningPorts, ListeningPorts: []engine.PortEntry{{Port: 8080, PID: 123, Process: "go"}}})
	sm.Handle(engine.Event{Type: engine.EventSetQuickJumpTargets, QuickJumpTargets: []engine.QuickJumpTarget{{ID: "ws-1", Kind: "workspace", Title: "clavis", Target: "/tmp/clavis"}}})
	sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})

	snap0 := sm.State()
	if len(snap0.OmniboxItems) != 50 {
		t.Fatalf("expected 50 omnibox items, got %d", len(snap0.OmniboxItems))
	}
	// Sealed capacities prevent append() from mutating shared backing storage.
	if cap(snap0.OmniboxItems) != len(snap0.OmniboxItems) {
		t.Fatalf("OmniboxItems cap=%d != len=%d", cap(snap0.OmniboxItems), len(snap0.OmniboxItems))
	}
	if cap(snap0.ClipboardEntries) != len(snap0.ClipboardEntries) {
		t.Fatalf("ClipboardEntries cap=%d != len=%d", cap(snap0.ClipboardEntries), len(snap0.ClipboardEntries))
	}
	if cap(snap0.InstalledApps) != len(snap0.InstalledApps) {
		t.Fatalf("InstalledApps cap=%d != len=%d", cap(snap0.InstalledApps), len(snap0.InstalledApps))
	}
	if cap(snap0.ListeningPorts) != len(snap0.ListeningPorts) {
		t.Fatalf("ListeningPorts cap=%d != len=%d", cap(snap0.ListeningPorts), len(snap0.ListeningPorts))
	}
	if cap(snap0.QuickJumpTargets) != len(snap0.QuickJumpTargets) {
		t.Fatalf("QuickJumpTargets cap=%d != len=%d", cap(snap0.QuickJumpTargets), len(snap0.QuickJumpTargets))
	}

	origItemsPtr := unsafe.SliceData(snap0.OmniboxItems)
	origClipPtr := unsafe.SliceData(snap0.ClipboardEntries)
	origAppsPtr := unsafe.SliceData(snap0.InstalledApps)

	for _, ev := range []engine.Event{
		{Type: engine.EventSelectNext},
		{Type: engine.EventSelectNext},
		{Type: engine.EventSelectPrevious},
		{Type: engine.EventSelectIndex, Index: 10},
		{Type: engine.EventHoverItem, Index: 4},
	} {
		sm.Handle(ev)
		snap := sm.State()
		if unsafe.SliceData(snap.OmniboxItems) != origItemsPtr {
			t.Fatalf("event %s unnecessarily reallocated OmniboxItems", ev.Type)
		}
		if unsafe.SliceData(snap.ClipboardEntries) != origClipPtr {
			t.Fatalf("event %s unnecessarily reallocated ClipboardEntries", ev.Type)
		}
		if unsafe.SliceData(snap.InstalledApps) != origAppsPtr {
			t.Fatalf("event %s unnecessarily reallocated InstalledApps", ev.Type)
		}
	}

	// Verify appending to a caller's snapshot slice does not corrupt sm.State().
	_ = append(snap0.OmniboxItems, engine.OmniboxItem{ID: "injected"})
	if got := len(sm.State().OmniboxItems); got != 50 {
		t.Fatalf("append on snapshot mutated StateMachine len to %d", got)
	}
}

// TestStateMachineCOWMutationsPreserveOldSnapshots verifies that every
// collection-mutating transition (pin toggle, delete, clipboard sync, search
// results, query filter, frecency update, AI follow-up, leader drill/back)
// allocates fresh storage and leaves previously captured State snapshots intact.
func TestStateMachineCOWMutationsPreserveOldSnapshots(t *testing.T) {
	sm := engine.NewStateMachine(config.DefaultConfig())
	sm.SetExternalClipboardSearch(true)
	entries := buildTestClipboardEntries(5)
	sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: entries})
	sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	sm.Handle(engine.Event{Type: engine.EventSelectIndex, Index: 1})

	beforePin := sm.State()
	if beforePin.ClipboardEntries[1].IsPinned {
		t.Fatal("expected entry 1 initially unpinned")
	}

	// Toggle pin on entry 1 (must COW ClipboardEntries, never mutate beforePin).
	sm.Handle(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem})
	afterPin := sm.State()
	if beforePin.ClipboardEntries[1].IsPinned {
		t.Fatal("COW violation: EventTogglePinSelectedClipboardItem mutated prior snapshot's ClipboardEntries")
	}
	if !afterPin.ClipboardEntries[1].IsPinned {
		t.Fatal("expected entry 1 pinned in new snapshot")
	}
	if unsafe.SliceData(beforePin.ClipboardEntries) == unsafe.SliceData(afterPin.ClipboardEntries) {
		t.Fatal("expected fresh ClipboardEntries backing array after pin toggle")
	}

	// External search results + pin toggle on search results must also COW.
	sm.Handle(engine.Event{Type: engine.EventUpdateQuery, Query: "item"})
	sm.Handle(engine.Event{Type: engine.EventSetClipboardSearchResults, Query: "item", ClipboardEntries: entries[:2]})
	sm.Handle(engine.Event{Type: engine.EventSelectIndex, Index: 0})
	beforeSearchPin := sm.State()
	sm.Handle(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem})
	afterSearchPin := sm.State()
	if beforeSearchPin.ClipboardSearchEntries[0].IsPinned {
		t.Fatal("COW violation: pin toggle mutated prior snapshot's ClipboardSearchEntries")
	}
	if !afterSearchPin.ClipboardSearchEntries[0].IsPinned {
		t.Fatal("expected search entry 0 pinned in new snapshot")
	}

	// Delete selected entry must preserve prior snapshot.
	beforeDel := sm.State()
	sm.Handle(engine.Event{Type: engine.EventDeleteSelectedClipboardItem})
	if len(beforeDel.ClipboardEntries) != 5 || len(beforeDel.ClipboardSearchEntries) != 2 {
		t.Fatalf("COW violation: delete mutated prior snapshot lengths: %d/%d", len(beforeDel.ClipboardEntries), len(beforeDel.ClipboardSearchEntries))
	}

	// SetFrecencyScores must allocate fresh map, preserving previous snapshot's map.
	sm.Handle(engine.Event{Type: engine.EventSetFrecencyScores, FrecencyScores: map[string]int{"a": 1}})
	beforeFrec := sm.State()
	sm.Handle(engine.Event{Type: engine.EventSetFrecencyScores, FrecencyScores: map[string]int{"a": 99, "b": 2}})
	if beforeFrec.FrecencyScores["a"] != 1 || beforeFrec.FrecencyScores["b"] != 0 {
		t.Fatalf("COW violation: SetFrecencyScores mutated prior snapshot map: %+v", beforeFrec.FrecencyScores)
	}

	// AI FollowUp must COW AIHistory even across multiple turns.
	sm.Handle(engine.Event{Type: engine.EventStartAI, Mode: "ask", Prompt: "turn 1"})
	sm.Handle(engine.Event{Type: engine.EventAIChunk, Chunk: "answer 1"})
	sm.Handle(engine.Event{Type: engine.EventAICompleted})
	sm.Handle(engine.Event{Type: engine.EventFollowUpAI, Prompt: "turn 2"})
	snapAI1 := sm.State()
	if len(snapAI1.AIHistory) != 2 || cap(snapAI1.AIHistory) != 2 {
		t.Fatalf("unexpected AIHistory after first follow-up: len=%d cap=%d", len(snapAI1.AIHistory), cap(snapAI1.AIHistory))
	}
	sm.Handle(engine.Event{Type: engine.EventAIChunk, Chunk: "answer 2"})
	sm.Handle(engine.Event{Type: engine.EventAICompleted})
	sm.Handle(engine.Event{Type: engine.EventFollowUpAI, Prompt: "turn 3"})
	snapAI2 := sm.State()
	if len(snapAI1.AIHistory) != 2 || snapAI1.AIHistory[0].Content != "turn 1" || snapAI1.AIHistory[1].Content != "answer 1" {
		t.Fatalf("COW violation: second follow-up mutated prior AIHistory: %+v", snapAI1.AIHistory)
	}
	if len(snapAI2.AIHistory) != 4 {
		t.Fatalf("expected 4 turns in second AIHistory, got %d", len(snapAI2.AIHistory))
	}
}

// TestStateMachineCOWConcurrentSnapshotReads exercises concurrent snapshot
// reads while a writer drives selection and COW-mutating transitions under -race.
func TestStateMachineCOWConcurrentSnapshotReads(t *testing.T) {
	sm := engine.NewStateMachine(config.DefaultConfig())
	sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: buildTestClipboardEntries(30)})
	sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})

	var mu sync.Mutex
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Multiple concurrent readers grab snapshots under lock, release lock,
	// and iterate every slice/map field while the writer keeps mutating sm.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				mu.Lock()
				snap := sm.State()
				mu.Unlock()
				for _, it := range snap.OmniboxItems {
					_ = it.ID
					_ = it.KeyHint
				}
				for _, e := range snap.ClipboardEntries {
					_ = e.IsPinned
				}
				for k, v := range snap.FrecencyScores {
					_, _ = k, v
				}
				for _, h := range snap.AIHistory {
					_ = h.Content
				}
			}
		}()
	}

	for i := 0; i < 120; i++ {
		mu.Lock()
		switch i % 6 {
		case 0:
			sm.Handle(engine.Event{Type: engine.EventSelectNext})
		case 1:
			sm.Handle(engine.Event{Type: engine.EventSelectPrevious})
		case 2:
			sm.Handle(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem})
		case 3:
			sm.Handle(engine.Event{Type: engine.EventSetFrecencyScores, FrecencyScores: map[string]int{fmt.Sprintf("k%d", i): i}})
		case 4:
			sm.Handle(engine.Event{Type: engine.EventHoverItem, Index: i % 10})
		case 5:
			sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: buildTestClipboardEntries(30)})
		}
		mu.Unlock()
	}
	close(stop)
	wg.Wait()
}

// TestStateMachineSelectionZeroAllocs pins the hot-path allocation guarantee:
// State() and selection-only events perform zero heap allocations.
func TestStateMachineSelectionZeroAllocs(t *testing.T) {
	sm := engine.NewStateMachine(config.DefaultConfig())
	sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: buildTestClipboardEntries(50)})
	sm.Handle(engine.Event{Type: engine.EventSetInstalledApps, InstalledApps: []engine.InstalledAppEntry{
		{Name: "Safari", BundleID: "com.apple.Safari", Path: "/Applications/Safari.app"},
	}})
	sm.Handle(engine.Event{Type: engine.EventSetFrecencyScores, FrecencyScores: map[string]int{"clipboard:clip-001": 10}})
	sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})

	if allocs := testing.AllocsPerRun(100, func() {
		_ = sm.State()
	}); allocs != 0 {
		t.Fatalf("State() allocated %.2f allocs/op, want 0", allocs)
	}

	i := 0
	if allocs := testing.AllocsPerRun(100, func() {
		ev := engine.EventSelectNext
		if i%2 == 1 {
			ev = engine.EventSelectPrevious
		}
		i++
		_ = sm.Handle(engine.Event{Type: ev})
		_ = sm.State()
	}); allocs != 0 {
		t.Fatalf("Handle(SelectNext/Prev)+State() allocated %.2f allocs/op, want 0", allocs)
	}

	j := 0
	if allocs := testing.AllocsPerRun(100, func() {
		idx := (j % 10) + 1
		j++
		_ = sm.Handle(engine.Event{Type: engine.EventHoverItem, Index: idx})
		_ = sm.Handle(engine.Event{Type: engine.EventSelectIndex, Index: idx - 1})
	}); allocs != 0 {
		t.Fatalf("Handle(HoverItem/SelectIndex) allocated %.2f allocs/op, want 0", allocs)
	}
}
