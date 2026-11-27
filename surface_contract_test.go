package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeisme/clavis/uiapp"
)

func TestSF05RaiseAboveOrder(t *testing.T) {
	var order []string
	got := raiseAboveOrder(uiapp.SurfaceElevationPanel, []surfaceZEntry{
		{name: "selection", elevation: uiapp.SurfaceElevationRaised, visible: true, showInactive: func() { order = append(order, "selection") }},
		{name: "capsule", elevation: uiapp.SurfaceElevationPanel, visible: true, showInactive: func() { order = append(order, "capsule") }},
		{name: "pill", elevation: uiapp.SurfaceElevationIndicator, visible: true, showInactive: func() { order = append(order, "pill") }},
	})
	want := []string{"pill"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(order, want) {
		t.Fatalf("raiseAboveOrder(panel) = (%v, %v), want %v", got, order, want)
	}

	order = nil
	got = raiseAboveOrder(uiapp.SurfaceElevationFlat, []surfaceZEntry{
		{name: "pill", elevation: uiapp.SurfaceElevationIndicator, visible: true, showInactive: func() { order = append(order, "pill") }},
		{name: "candidate", elevation: uiapp.SurfaceElevationPanel, visible: true, showInactive: func() { order = append(order, "candidate") }},
		{name: "hidden_pill", elevation: uiapp.SurfaceElevationIndicator + 1, visible: false, showInactive: func() { order = append(order, "hidden_pill") }},
		{name: "selection", elevation: uiapp.SurfaceElevationRaised, visible: true, showInactive: func() { order = append(order, "selection") }},
	})
	wantAsc := []string{"selection", "candidate", "pill"}
	if !reflect.DeepEqual(got, wantAsc) || !reflect.DeepEqual(order, wantAsc) {
		t.Fatalf("raiseAboveOrder(flat) = (%v, %v), want ascending %v", got, order, wantAsc)
	}
}

func TestSF08BindSurfaceAndSingleTransitionCheck(t *testing.T) {
	for _, file := range []string{"main.go", "input_engine_ui.go", "onboarding.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(src)
		if strings.Contains(text, ".OnClose(") || strings.Contains(text, ".OnBlur(") {
			t.Fatalf("%s must not register OnClose/OnBlur directly; use bindSurface -> BindFloatingSurface", file)
		}
		if file == "main.go" && strings.Contains(text, "closeHides") {
			t.Fatalf("main.go must remove legacy closeHides helper")
		}
	}

	// Present-transition check lives only once inside bindSurface (plus helper definition).
	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if count := strings.Count(string(mainSrc), "isInPresentTransition"); count != 2 {
		t.Fatalf("main.go references isInPresentTransition %d times, want 2 (1 helper def + 1 bindSurface wire)", count)
	}

	kitSrc, err := os.ReadFile(filepath.Join("uiapp", "kit_surface.go"))
	if err != nil {
		t.Fatalf("read uiapp/kit_surface.go: %v", err)
	}
	kitText := string(kitSrc)
	if strings.Count(kitText, ".OnClose(") != 1 || strings.Count(kitText, ".OnBlur(") != 1 {
		t.Fatalf("uiapp/kit_surface.go should register OnClose once and OnBlur once")
	}
}

func TestSF11SF12SF13DocumentFocusRestore(t *testing.T) {
	// SF-13: another document window is still open -> keep focus in Clavis.
	activated := ""
	hidden := false
	if restoreDocumentFocus(true, false, "com.apple.Safari", func(b string) error {
		activated = b
		return nil
	}, func() { hidden = true }) {
		t.Fatal("SF-13: expected no external restore while second document window is visible")
	}
	if activated != "" || hidden {
		t.Fatalf("SF-13: unexpectedly activated=%q or hidden=%v", activated, hidden)
	}

	// SF-11: last document window closed -> reactivate recorded Safari bundle.
	if !restoreDocumentFocus(false, true, "com.apple.Safari", func(b string) error {
		activated = b
		return nil
	}, func() { hidden = true }) {
		t.Fatal("SF-11: expected Safari activation")
	}
	if activated != "com.apple.Safari" || hidden {
		t.Fatalf("SF-11: activated=%q hidden=%v, want Safari activated without App.Hide", activated, hidden)
	}

	// SF-12a: target exited while floating pill visible -> never call App.Hide.
	hidden = false
	if restoreDocumentFocus(false, true, "com.apple.Safari", func(string) error {
		return errors.New("app exited")
	}, func() { hidden = true }) {
		t.Fatal("SF-12: should not hide Clavis when floating surface is visible")
	}
	if hidden {
		t.Fatal("SF-12: App.Hide called while floating surface was visible")
	}

	// SF-12b: target exited and no floating surface visible -> App.Hide releases active state.
	hidden = false
	if !restoreDocumentFocus(false, false, "com.apple.Safari", func(string) error {
		return errors.New("app exited")
	}, func() { hidden = true }) {
		t.Fatal("SF-12: expected App.Hide fallback when no surfaces remain visible")
	}
	if !hidden {
		t.Fatal("SF-12: App.Hide not invoked when target exited and no floating surface open")
	}
}
