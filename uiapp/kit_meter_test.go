package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/storage"
)

// TestQuotaMeterMath pins the meter: used/limit clamped to [0, 1], neutral
// gray below 90% and system orange from there, and image bytes summed from
// the kind stats the way the media quota counts them.
func TestQuotaMeterMath(t *testing.T) {
	cases := []struct{ used, limit, want float64 }{
		{0, 512, 0}, {256, 512, 0.5}, {600, 512, 1}, {10, 0, 0}, {-3, 10, 0},
	}
	for _, tc := range cases {
		if got := quotaMeterRatio(tc.used, tc.limit); got != tc.want {
			t.Fatalf("quotaMeterRatio(%v, %v) = %v, want %v", tc.used, tc.limit, got, tc.want)
		}
	}
	light := macTheme(false, ui.Preferences{})
	if quotaMeterFill(light, 0.5) == kitStateWarningColor(light) {
		t.Fatal("a half-used quota already reads as a warning")
	}
	if quotaMeterFill(light, 0.95) != kitStateWarningColor(light) {
		t.Fatal("a 95% quota does not turn to the warning color")
	}
	stats := []storage.ClipboardKindStat{{Kind: "image", Count: 3, Bytes: 3 << 20}, {Kind: "text", Count: 9, Bytes: 900}, {Kind: "image", Count: 1, Bytes: 1 << 20}}
	if got := clipboardKindBytes(stats, "image"); got != 4<<20 {
		t.Fatalf("image bytes %d, want %d", got, 4<<20)
	}
}
