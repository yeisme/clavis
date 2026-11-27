package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

	// umask first: Linux CI runs with 002 (giving 775 directories) while macOS runs with 022. os.MkdirTemp asks
	// for 0700, but the umask can only widen nothing and narrow everything -- 002 does not widen 0700 -- so the
	// real problem is elsewhere: t.TempDir() creates its own subdirectories with 0777&^umask, which is 0775 under
	// 002 and therefore group-writable. Setting the umask here makes every directory this package's tests create
	// private, on both platforms, without touching the production guard.
	previousUmask := syscall.Umask(0o077)
	_ = previousUmask // process-wide for the rest of the run; restored implicitly when the test binary exits

	base, err := os.MkdirTemp("", "clavis-storage-tests-")
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
