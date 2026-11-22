package uiapp

import (
	"testing"
	"time"
)

func TestFormatSyncRelativeTime(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "never", want: "从未同步"},
		{name: "recent", at: now.Add(-20 * time.Second), want: "刚刚"},
		{name: "minutes", at: now.Add(-5 * time.Minute), want: "5 分钟前"},
		{name: "hours", at: now.Add(-2 * time.Hour), want: "2 小时前"},
		{name: "days", at: now.Add(-3 * 24 * time.Hour), want: "3 天前"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatSyncRelativeTime(tc.at, now); got != tc.want {
				t.Fatalf("formatSyncRelativeTime(%v) = %q, want %q", tc.at, got, tc.want)
			}
		})
	}
}
