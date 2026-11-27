package sync

import (
	"fmt"
	"os"
	"testing"
)

// TestMain pins this package's test environment so the same tests mean the same thing on macOS, Linux and Windows.
//
// Two platform differences had to be neutralised, both of them the suite encoding one host's defaults:
//
//  1. TMPDIR. The sync store refuses to create a database under any EXISTING ancestor that is group/world-writable
//     (mkdirPrivate walks upward and validates each one). t.TempDir() honours TMPDIR: on macOS it is /var/folders/...
//     (0700) and everything passes, while on Linux TMPDIR is unset so t.TempDir() falls back to /tmp, which is 1777 —
//     and the store is right to refuse it. The guard stays untouched; the test base is moved under $HOME (0700 on
//     every platform) instead.
//  2. umask. Windows has no umask, Linux CI runs with 002, macOS with 022. Under 002 the directories t.TempDir()
//     creates come out 0775 (still group-writable, still rejected) and files come out 0664 where assertions expect
//     0644. Pinning 022 — the value this suite was written against, and the macOS default — removes the difference.
//     See umask_unix_test.go; the Windows build has no umask to pin.
func TestMain(m *testing.M) {
	previous, hadPrevious := os.LookupEnv("TMPDIR")

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		fmt.Fprintf(os.Stderr, "sync tests: cannot resolve a home directory for the private temp base: %v\n", err)
		os.Exit(1)
	}
	base, err := os.MkdirTemp(home, ".clavis-tests-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync tests: cannot create a private temp base: %v\n", err)
		os.Exit(1)
	}
	if err := os.Chmod(base, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "sync tests: cannot tighten the temp base: %v\n", err)
		_ = os.RemoveAll(base)
		os.Exit(1)
	}
	if err := os.Setenv("TMPDIR", base); err != nil {
		fmt.Fprintf(os.Stderr, "sync tests: cannot set TMPDIR: %v\n", err)
		_ = os.RemoveAll(base)
		os.Exit(1)
	}
	pinUmaskForTests()

	code := m.Run()

	if hadPrevious {
		_ = os.Setenv("TMPDIR", previous)
	} else {
		_ = os.Unsetenv("TMPDIR")
	}
	_ = os.RemoveAll(base)
	os.Exit(code)
}
