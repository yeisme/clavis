package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain gives every t.TempDir() in this package a parent that is not group/world-writable.
//
// Why: the store refuses to create a database under a directory whose existing ancestors are 0022-writable
// (validatePrivateDirectory). t.TempDir() honours TMPDIR, so on macOS it lands under $TMPDIR (/var/folders/...
// with mode 700) and every test passes. On Linux TMPDIR is unset, t.TempDir() falls back to /tmp, and /tmp is
// 1777 — so the same tests fail on CI with "create database directory: /tmp is group/world-writable (mode 777)"
// while passing locally. The guard is correct and must not be relaxed; the test environment is what has to
// provide a private parent.
func TestMain(m *testing.M) {
	previous, hadPrevious := os.LookupEnv("TMPDIR")

	// The base must NOT live under /tmp. mkdirPrivate walks every existing ancestor of the database path and rejects
	// any that is group/world-writable, and /tmp is 1777 on Linux -- so a TMPDIR under /tmp fails no matter what
	// permissions the directories below it have. $HOME is 0700+ on both platforms, which is what the guard wants to
	// see. TestMain puts the base there and points TMPDIR at it.
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		fmt.Fprintf(os.Stderr, "storage tests: cannot resolve a home directory for the private temp base: %v\n", err)
		os.Exit(1)
	}
	base, err := os.MkdirTemp(home, "clavis-storage-tests-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage tests: cannot create a private temp base: %v\n", err)
		os.Exit(1)
	}
	// MkdirTemp already creates 0700, but be explicit: a umask or a platform default must not widen it.
	if err := os.Chmod(base, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "storage tests: cannot tighten temp base permissions: %v\n", err)
		_ = os.RemoveAll(base)
		os.Exit(1)
	}
	if err := os.Setenv("TMPDIR", base); err != nil {
		fmt.Fprintf(os.Stderr, "storage tests: cannot set TMPDIR: %v\n", err)
		_ = os.RemoveAll(base)
		os.Exit(1)
	}

	code := m.Run()

	if hadPrevious {
		_ = os.Setenv("TMPDIR", previous)
	} else {
		_ = os.Unsetenv("TMPDIR")
	}
	_ = os.RemoveAll(base)
	os.Exit(code)
}

// privateTempDir is the explicit form for a test that needs a directory whose own mode matters, not just its
// parents: it is created under the private TMPDIR that TestMain installed.
func privateTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "clavis-private-")
	if err != nil {
		t.Fatalf("cannot create a private temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("cannot tighten %s: %v", filepath.Base(dir), err)
	}
	return dir
}
