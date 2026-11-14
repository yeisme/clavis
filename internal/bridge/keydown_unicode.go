package bridge

// keyDownUnicodeCache caches at most one ToUnicodeEx translation within a single key-down
// event so the own input engine, inline IME, and trigger router share the same result
// without calling ToUnicodeEx multiple times (which mutates Win32 dead-key / Alt+Numpad state).
type keyDownUnicodeCache struct {
	vk       uint16
	scanCode uint32
	resolved bool
	text     string
	lookup   func(vk uint16, scanCode uint32) string
}

func (c *keyDownUnicodeCache) Resolve(vk uint16, scanCode uint32) string {
	if c == nil {
		return ""
	}
	if c.resolved && c.vk == vk && c.scanCode == scanCode {
		return c.text
	}
	c.vk = vk
	c.scanCode = scanCode
	c.resolved = true
	if c.lookup != nil {
		c.text = c.lookup(vk, scanCode)
	} else {
		c.text = ""
	}
	return c.text
}

type winKeyDownHooks struct {
	inputEngine func() bool
	onFallback  func()
	inline      func(unicodeStr string) bool
	router      func(unicodeStr string) bool
}

// dispatchWinKeyDown runs the key-down pipeline (input engine -> inline IME -> trigger router)
// using cache so lookup is invoked at most once per physical key-down.
func dispatchWinKeyDown(vk uint16, scanCode uint32, cache *keyDownUnicodeCache, hooks winKeyDownHooks) bool {
	if hooks.inputEngine != nil && hooks.inputEngine() {
		return true
	}
	if hooks.onFallback != nil {
		hooks.onFallback()
	}
	unicodeStr := cache.Resolve(vk, scanCode)
	if hooks.inline != nil && hooks.inline(unicodeStr) {
		return true
	}
	if hooks.router != nil && hooks.router(unicodeStr) {
		return true
	}
	return false
}
