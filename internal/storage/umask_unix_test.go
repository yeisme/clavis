//go:build !windows

package storage

import "syscall"

// pinUmaskForTests sets umask 022 for the lifetime of this test binary.
//
// 022 rather than the stricter 077: 077 makes created files 0600 and breaks assertions that expect 0644. 022 is
// what the suite was written against and is the macOS default, so the Linux runner (which uses 002) ends up
// meaning the same thing as a developer machine. Process-wide, and lasts until the binary exits.
func pinUmaskForTests() {
	_ = syscall.Umask(0o022)
}
