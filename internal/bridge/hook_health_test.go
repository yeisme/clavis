package bridge

import (
	"testing"
	"time"
)

func TestHookWatchdogHealthyAndMouseActivity(t *testing.T) {
	now := time.Unix(1000, 0)
	w := hookWatchdog{mouseHook: true}
	// Hook saw the latest input, or lag within 2s: never reinstall.
	for _, c := range [][2]uint32{{5000, 5000}, {5000, 4000}, {5000, 3000}, {5000, 6000}} {
		if w.Observe(c[0], c[1], now) || w.Observe(c[0], c[1], now) {
			t.Fatalf("healthy %v reinstalled", c)
		}
	}
	// Without a mouse hook, mouse-only input (10s lag) must not look like a dead keyboard hook.
	w = hookWatchdog{mouseHook: false}
	if w.Observe(20000, 10000, now) || w.Observe(20000, 10000, now) {
		t.Fatal("10s lag without mouse hook should be tolerated")
	}
	if w.Observe(50000, 10000, now); !w.Observe(50000, 10000, now) {
		t.Fatal("40s lag without mouse hook should reinstall")
	}
}

func TestHookWatchdogNeedsTwoBeatsAndBacksOff(t *testing.T) {
	now := time.Unix(1000, 0)
	w := hookWatchdog{mouseHook: true}
	if w.Observe(10000, 1000, now) {
		t.Fatal("first stale beat must not reinstall")
	}
	if !w.Observe(12000, 1000, now) {
		t.Fatal("second stale beat should reinstall")
	}
	w.Reinstalled(false, now)
	w.Observe(14000, 1000, now)
	if w.Observe(16000, 1000, now.Add(time.Second)) {
		t.Fatal("within 2s backoff after failure")
	}
	if !w.Observe(18000, 1000, now.Add(3*time.Second)) {
		t.Fatal("after backoff should retry")
	}
	w.Reinstalled(false, now)
	if w.backoff != 4*time.Second {
		t.Fatalf("backoff = %v", w.backoff)
	}
	for range 10 {
		w.Reinstalled(false, now)
	}
	if w.backoff != hookReinstallMaxBackoff {
		t.Fatalf("backoff cap = %v", w.backoff)
	}
	w.Reinstalled(true, now)
	if w.backoff != 0 || !w.nextTry.IsZero() {
		t.Fatal("success resets backoff")
	}
}

func TestHookWatchdogTickWraparound(t *testing.T) {
	w := hookWatchdog{mouseHook: true}
	now := time.Unix(1000, 0)
	// lastHook just before wrap, lastInput just after: lag = 200ms → healthy.
	if w.Observe(100, ^uint32(0)-99, now) || w.Observe(100, ^uint32(0)-99, now) {
		t.Fatal("wraparound misread as stale")
	}
}

func TestIsMouseButtonDown(t *testing.T) {
	for wp, want := range map[uintptr]bool{0x201: true, 0x204: true, 0x200: false, 0x202: false, 0x20A: false} {
		if isMouseButtonDown(wp) != want {
			t.Fatalf("%#x", wp)
		}
	}
}

func TestIsSecureField(t *testing.T) {
	cases := []struct {
		s    secureFieldSignals
		want bool
	}{
		{secureFieldSignals{ClassName: "Edit", Style: esPassword}, true},
		{secureFieldSignals{ClassName: "Edit", PasswordChar: '*'}, true},
		{secureFieldSignals{ClassName: "WindowsForms10.EDIT.app.0.141b42a_r6_ad1", Style: esPassword | 0x50010000}, true},
		{secureFieldSignals{ClassName: "RichEdit20W", Style: 0x50010000}, false},
		{secureFieldSignals{ClassName: "Edit", Style: 0x50010000}, false},
		// ES_PASSWORD bit on a non-edit class (e.g. a button style) is not a password box.
		{secureFieldSignals{ClassName: "Button", Style: esPassword}, false},
		{secureFieldSignals{ClassName: "Chrome_RenderWidgetHostHWND", ProcessName: "KeePassXC.exe"}, true},
		{secureFieldSignals{ProcessName: "CredentialUIBroker.exe"}, true},
		{secureFieldSignals{ClassName: "Chrome_RenderWidgetHostHWND", ProcessName: "chrome.exe"}, false},
	}
	for i, c := range cases {
		if got := isSecureField(c.s); got != c.want {
			t.Fatalf("case %d %+v = %v", i, c.s, got)
		}
	}
}

func TestCachedFlagFreshness(t *testing.T) {
	var f cachedFlag
	now := time.Unix(1000, 0)
	if _, fresh := f.Load(now, time.Second); fresh {
		t.Fatal("never stored must be stale")
	}
	f.Store(true, now)
	if v, fresh := f.Load(now.Add(500*time.Millisecond), time.Second); !v || !fresh {
		t.Fatal("fresh true")
	}
	if _, fresh := f.Load(now.Add(2*time.Second), time.Second); fresh {
		t.Fatal("should be stale")
	}
}
