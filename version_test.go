package main

import (
	"runtime/debug"
	"testing"
)

func TestFormatVersionLine(t *testing.T) {
	tests := []struct {
		name   string
		ver    string
		goos   string
		goarch string
		want   string
	}{
		{
			name:   "release version on darwin",
			ver:    "0.2.0",
			goos:   "darwin",
			goarch: "arm64",
			want:   "Clavis 0.2.0 (darwin/arm64)",
		},
		{
			name:   "dev version with vcs suffix on windows",
			ver:    "0.1.0-dev+316681964ea3.dirty",
			goos:   "windows",
			goarch: "amd64",
			want:   "Clavis 0.1.0-dev+316681964ea3.dirty (windows/amd64)",
		},
		{
			name:   "trims surrounding whitespace",
			ver:    "  1.0.0-rc1 \n",
			goos:   "linux",
			goarch: "amd64",
			want:   "Clavis 1.0.0-rc1 (linux/amd64)",
		},
		{
			name:   "empty version falls back to default",
			ver:    "   ",
			goos:   "darwin",
			goarch: "arm64",
			want:   "Clavis 0.1.0-dev (darwin/arm64)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatVersionLine(tc.ver, tc.goos, tc.goarch); got != tc.want {
				t.Fatalf("formatVersionLine(%q, %q, %q) = %q, want %q", tc.ver, tc.goos, tc.goarch, got, tc.want)
			}
		})
	}
}

func TestClavisVersionInjectedOverride(t *testing.T) {
	orig := clavisBuildVersion
	t.Cleanup(func() {
		clavisBuildVersion = orig
	})

	clavisBuildVersion = "0.2.0-test"
	if got := clavisVersion(); got != "0.2.0-test" {
		t.Fatalf("clavisVersion() with injected override = %q, want %q", got, "0.2.0-test")
	}

	clavisBuildVersion = "  0.3.0-rc2  "
	if got := clavisVersion(); got != "0.3.0-rc2" {
		t.Fatalf("clavisVersion() with trimmed override = %q, want %q", got, "0.3.0-rc2")
	}

	clavisBuildVersion = defaultBuildVersion
	got := clavisVersion()
	if got == "" {
		t.Fatalf("clavisVersion() with defaultBuildVersion returned empty string")
	}
	if got != defaultBuildVersion && len(got) <= len(defaultBuildVersion+"+") {
		t.Fatalf("clavisVersion() with defaultBuildVersion = %q, expected default or VCS-suffixed version", got)
	}
}

func TestResolveClavisVersionVCSFallback(t *testing.T) {
	fullRev := "316681964ea3abcdef0123456789abcdef012345"

	tests := []struct {
		name         string
		buildVersion string
		info         *debug.BuildInfo
		ok           bool
		want         string
	}{
		{
			name:         "injected release version ignores vcs metadata",
			buildVersion: "0.2.0",
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: fullRev},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			ok:   true,
			want: "0.2.0",
		},
		{
			name:         "default dev version uses truncated clean vcs revision",
			buildVersion: defaultBuildVersion,
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: fullRev},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			ok:   true,
			want: "0.1.0-dev+316681964ea3",
		},
		{
			name:         "default dev version marks dirty vcs revision",
			buildVersion: defaultBuildVersion,
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: fullRev},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			ok:   true,
			want: "0.1.0-dev+316681964ea3.dirty",
		},
		{
			name:         "short vcs revision preserved without truncation",
			buildVersion: defaultBuildVersion,
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc1234"},
				},
			},
			ok:   true,
			want: "0.1.0-dev+abc1234",
		},
		{
			name:         "empty build version falls back to vcs revision",
			buildVersion: "",
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "deadbeef12345678"},
				},
			},
			ok:   true,
			want: "0.1.0-dev+deadbeef1234",
		},
		{
			name:         "missing vcs revision falls back to default dev version",
			buildVersion: defaultBuildVersion,
			info:         &debug.BuildInfo{},
			ok:           true,
			want:         defaultBuildVersion,
		},
		{
			name:         "unavailable build info falls back to default dev version",
			buildVersion: defaultBuildVersion,
			info:         nil,
			ok:           false,
			want:         defaultBuildVersion,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveClavisVersion(tc.buildVersion, func() (*debug.BuildInfo, bool) {
				return tc.info, tc.ok
			})
			if got != tc.want {
				t.Fatalf("resolveClavisVersion(%q) = %q, want %q", tc.buildVersion, got, tc.want)
			}
		})
	}
}

func TestHasVersionFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "double dash version", args: []string{"--version"}, want: true},
		{name: "single dash version", args: []string{"-version"}, want: true},
		{name: "after another flag", args: []string{"--verbose", "--version"}, want: true},
		{name: "after terminator ignored", args: []string{"--", "--version"}, want: false},
		{name: "unrelated flags", args: []string{"--smoke-check", "--self-check"}, want: false},
		{name: "empty args", args: nil, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasVersionFlag(tc.args); got != tc.want {
				t.Fatalf("hasVersionFlag(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
