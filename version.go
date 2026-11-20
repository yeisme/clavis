package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

// clavisBuildVersion is replaced by scripts/package.sh in the generated build
// source. The fallback keeps ordinary development binaries identifiable.
const clavisBuildVersion = "0.1.0-dev"

func init() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" {
			fmt.Printf("Clavis %s (%s/%s)\n", clavisVersion(), runtime.GOOS, runtime.GOARCH)
			os.Exit(0)
		}
		if arg == "--" {
			return
		}
	}
}

func clavisVersion() string {
	if clavisBuildVersion != "0.1.0-dev" {
		return clavisBuildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var revision string
		var modified bool
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
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
			return "0.1.0-dev+" + revision
		}
	}
	return strings.TrimSpace(clavisBuildVersion)
}
