package bridge

// Platform-neutral helpers for the Windows IME integration. They live in an untagged file so the
// logic (HKL parsing, labels, conversion-state tracking) is unit-tested on every OS; the Win32
// calls that feed them are in bridge_windows.go.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yeisme/clavis/pkg/core/host"
)

// winIMEState is a snapshot of the foreground thread's keyboard layout and IMM conversion state.
type winIMEState struct {
	HKL uint64
	// ConvKnown is false when no IME window answered (no IMM context, hung app, etc.).
	ConvKnown bool
	Open      bool
	Native    bool
}

// FormatHKLID renders an HKL in the canonical input source ID form ("0x08040804").
func FormatHKLID(hkl uint64) string {
	return fmt.Sprintf("0x%08x", hkl)
}

// ParseHKLID parses "0x08040804", "08040804" or a bare LANGID ("0x0409") into a numeric HKL.
// isLangOnly reports a 16-bit LANGID that must be resolved against installed layouts.
func ParseHKLID(id string) (hkl uint64, isLangOnly bool, err error) {
	trimmed := strings.TrimSpace(strings.ToLower(id))
	trimmed = strings.TrimPrefix(trimmed, "0x")
	if trimmed == "" {
		return 0, false, fmt.Errorf("windows: empty HKL input source id")
	}
	val, err := strconv.ParseUint(trimmed, 16, 64)
	if err != nil {
		return 0, false, fmt.Errorf("windows: invalid HKL input source id %q: %w", id, err)
	}
	return val, val <= 0xFFFF, nil
}

// hklLangID returns the LANGID (low word) of an HKL.
func hklLangID(hkl uint64) uint16 { return uint16(hkl & 0xFFFF) }

// winLangIDIsCJKIME reports LANGIDs whose layouts are IMEs with a native/ASCII conversion mode
// (Chinese, Japanese, Korean). Other layouts (en-US, de-DE, ...) have no conversion mode.
func winLangIDIsCJKIME(langID uint16) bool {
	switch langID & 0x3FF { // primary language
	case 0x04, 0x11, 0x12: // LANG_CHINESE, LANG_JAPANESE, LANG_KOREAN
		return true
	}
	return false
}

// winLangTag maps a LANGID to a BCP-47 style tag for the languages Clavis labels specially.
func winLangTag(langID uint16) string {
	switch langID {
	case 0x0804:
		return "zh-CN"
	case 0x0404:
		return "zh-TW"
	case 0x0C04:
		return "zh-HK"
	case 0x1004:
		return "zh-SG"
	case 0x0411:
		return "ja-JP"
	case 0x0412:
		return "ko-KR"
	case 0x0409:
		return "en-US"
	case 0x0809:
		return "en-GB"
	}
	return fmt.Sprintf("0x%04x", langID)
}

// winInputSourceInfo builds InputSourceInfo from an HKL, a friendly name (may be empty) and the
// live conversion state. For CJK IMEs the Mode/IsASCII follow the conversion mode so Microsoft
// Pinyin in English mode reports ASCII; other layouts are ASCII only when they are English.
func winInputSourceInfo(hkl uint64, friendlyName string, st winIMEState) host.InputSourceInfo {
	langID := hklLangID(hkl)
	name := strings.TrimSpace(friendlyName)
	if name == "" {
		name = fmt.Sprintf("HKL 0x%04x", langID)
	}
	info := host.InputSourceInfo{
		ID:       FormatHKLID(hkl),
		Name:     name,
		Language: winLangTag(langID),
		IsASCII:  langID&0x3FF == 0x09, // LANG_ENGLISH
	}
	if winLangIDIsCJKIME(langID) && st.ConvKnown {
		if st.Open && st.Native {
			info.Mode = host.InputModeNative
			info.IsASCII = false
		} else {
			info.Mode = host.InputModeASCII
			info.IsASCII = true
		}
	}
	return info
}

// winIMEModeSatisfied reports whether the observed state already satisfies the requested mode.
func winIMEModeSatisfied(st winIMEState, wantNative bool) bool {
	if !st.ConvKnown {
		return false
	}
	if wantNative {
		return st.Open && st.Native
	}
	return !st.Open || !st.Native
}

// winIMEStateKey identifies the observable state the floating pill reflects.
func winIMEStateKey(info host.InputSourceInfo) string {
	return info.ID + "|" + info.Mode + "|" + strconv.FormatBool(info.IsASCII)
}

// imeChangeTracker detects input-source / conversion-mode changes between polls. The first
// observation primes the tracker without reporting a change.
type imeChangeTracker struct {
	last   string
	primed bool
}

// Observe records info and reports whether it differs from the previous observation.
func (t *imeChangeTracker) Observe(info host.InputSourceInfo) bool {
	key := winIMEStateKey(info)
	if !t.primed {
		t.primed = true
		t.last = key
		return false
	}
	if key == t.last {
		return false
	}
	t.last = key
	return true
}

// winTargetHKLForMode chooses an installed layout for a mode switch fallback: an English layout
// for ASCII (preferring preferredASCII when installed) or a CJK IME layout for native.
func winTargetHKLForMode(installed []uint64, wantNative bool, preferredASCII string) (uint64, bool) {
	if !wantNative && preferredASCII != "" {
		if want, langOnly, err := ParseHKLID(preferredASCII); err == nil {
			for _, h := range installed {
				if (!langOnly && h == want) || (langOnly && uint64(hklLangID(h)) == want) {
					return h, true
				}
			}
		}
	}
	for _, h := range installed {
		lang := hklLangID(h)
		if wantNative && winLangIDIsCJKIME(lang) {
			return h, true
		}
		if !wantNative && lang&0x3FF == 0x09 {
			return h, true
		}
	}
	return 0, false
}

// resolveInstalledHKL maps an input source id to an installed HKL. A bare LANGID matches the
// first installed layout with that language; a full HKL must be installed.
func resolveInstalledHKL(installed []uint64, id string) (uint64, error) {
	want, langOnly, err := ParseHKLID(id)
	if err != nil {
		return 0, err
	}
	for _, h := range installed {
		if (!langOnly && h == want) || (langOnly && uint64(hklLangID(h)) == want) {
			return h, nil
		}
	}
	return 0, fmt.Errorf("windows: input source %s is not installed", id)
}
