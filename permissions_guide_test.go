package main

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/host"
)

// A machine that finished the guide without granting must be greeted again, otherwise the
// keyboard features stay dead with no explanation and nothing ever asks for the grant.
func TestPermissionsRequiredMissing(t *testing.T) {
	cases := []struct {
		name string
		st   host.PermissionStatus
		want bool
	}{
		{"both granted", host.PermissionStatus{Accessibility: true, InputMonitoring: true, Granted: true}, false},
		{"no accessibility", host.PermissionStatus{Accessibility: false, InputMonitoring: true}, true},
		{"no input monitoring", host.PermissionStatus{Accessibility: true, InputMonitoring: false}, true},
		{"neither", host.PermissionStatus{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := permissionsRequiredMissing(tc.st); got != tc.want {
				t.Fatalf("permissionsRequiredMissing(%+v) = %v, want %v", tc.st, got, tc.want)
			}
		})
	}
}
