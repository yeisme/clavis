package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

const defaultBuildVersion = "0.1.0-dev"

// clavisBuildVersion is injected at link time via:
//
//	go build -ldflags "-X main.clavisBuildVersion=<version>"
//
// or via MYGO_LDFLAGS="-X main.clavisBuildVersion=<version>" for `mygo build`.
// The fallback keeps ordinary development binaries identifiable without
// mutating tracked source files.
var clavisBuildVersion = defaultBuildVersion

func init() {
	if hasVersionFlag(os.Args[1:]) {
		fmt.Println(formatVersionLine(clavisVersion(), runtime.GOOS, runtime.GOARCH))
		os.Exit(0)
	}
}

func hasVersionFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--version" || arg == "-version" {
			return true
		}
		if arg == "--" {
			return false
		}
	}
	return false
}

func formatVersionLine(ver, goos, goarch string) string {
	ver = strings.TrimSpace(ver)
	if ver == "" {
		ver = defaultBuildVersion
	}
	return fmt.Sprintf("Clavis %s (%s/%s)", ver, goos, goarch)
}

func clavisVersion() string {
	return resolveClavisVersion(clavisBuildVersion, debug.ReadBuildInfo)
}

func resolveClavisVersion(buildVersion string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if v := strings.TrimSpace(buildVersion); v != "" && v != defaultBuildVersion {
		return v
	}
	if readBuildInfo != nil {
		if info, ok := readBuildInfo(); ok && info != nil {
			var revision string
			var modified bool
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					revision = strings.TrimSpace(setting.Value)
				case "vcs.modified":
					modified = setting.Value == "true"
				}
			}
			if revision != "" {
				if len(revision) > 12 {
					revision = revision[:12]
				}
				if modified {
					revision += ".dirty"
				}
				return defaultBuildVersion + "+" + revision
			}
		}
	}
	return defaultBuildVersion
}
