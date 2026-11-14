package bridge

import "testing"

func TestKeyDownUnicodeCache_SingleToUnicodeExPreservesDeadKey(t *testing.T) {
	// Simulate Win32 ToUnicodeEx where a pending dead key ('^') is consumed on the FIRST call
	// (returning "â") and cleared afterwards (a second call for the same key-down returns "a").
	deadPending := true
	calls := 0
	toUnicodeEx := func(vk uint16, scan uint32) string {
		calls++
		if vk == 'A' {
			if deadPending {
				deadPending = false
				return "â"
			}
			return "a"
		}
		return ""
	}

	cache := &keyDownUnicodeCache{lookup: toUnicodeEx}
	var engineSaw, inlineSaw, routerSaw string

	consumed := dispatchWinKeyDown('A', 0x1E, cache, winKeyDownHooks{
		inputEngine: func() bool {
			// Own-engine translates the key (e.g. via winVKToUnicode -> cache.Resolve),
			// sees a non-ASCII/dead-composed rune while idle, and passes it through.
			engineSaw = cache.Resolve('A', 0x1E)
			return false
		},
		inline: func(u string) bool {
			inlineSaw = u
			return false
		},
		router: func(u string) bool {
			routerSaw = u
			return false
		},
	})

	if consumed {
		t.Fatal("expected key not to be consumed")
	}
	if calls != 1 {
		t.Fatalf("ToUnicodeEx called %d times on a single key-down, want 1", calls)
	}
	if engineSaw != "â" || inlineSaw != "â" || routerSaw != "â" {
		t.Fatalf("dead-key output corrupted across stages: engine=%q inline=%q router=%q", engineSaw, inlineSaw, routerSaw)
	}

	// When inputEngine consumes the key, fallback paths never run and lookup still runs once.
	calls = 0
	deadPending = true
	cache2 := &keyDownUnicodeCache{lookup: toUnicodeEx}
	inlineCalled := false
	routerCalled := false
	consumed = dispatchWinKeyDown('A', 0x1E, cache2, winKeyDownHooks{
		inputEngine: func() bool {
			_ = cache2.Resolve('A', 0x1E)
			return true
		},
		inline: func(string) bool {
			inlineCalled = true
			return false
		},
		router: func(string) bool {
			routerCalled = true
			return false
		},
	})
	if !consumed || calls != 1 || inlineCalled || routerCalled {
		t.Fatalf("consumed=%v calls=%d inline=%v router=%v", consumed, calls, inlineCalled, routerCalled)
	}
}
