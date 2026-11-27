//go:build windows

package main

// pinUmaskForTests is a no-op on Windows: there is no umask, and NTFS ACLs are not what these assertions measure.
func pinUmaskForTests() {}
