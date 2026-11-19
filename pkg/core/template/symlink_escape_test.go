package template

import (
	"os"
	"path/filepath"
	"testing"
)

// T2 自述「磁盘隔离专项审查未出结论」。这里补上：在 root 内放一个指向 root 外的符号链接，
// 断言写入不会逃逸。os.Root 应该拦住它。
func TestEscapeSymlinkIsContained(t *testing.T) {
	outside := t.TempDir()
	rootDir := t.TempDir()

	// root/esc -> outside
	if err := os.Symlink(outside, filepath.Join(rootDir, "esc")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	lib, err := OpenLibrary(rootDir)
	if err != nil {
		t.Fatalf("OpenLibrary: %v", err)
	}
	defer lib.Close()

	// 试图通过符号链接写到 root 外
	if werr := lib.atomicWrite("esc/pwned.txt", []byte("x")); werr == nil {
		// 写成功也不算失败——只要没落到 outside 就行
		t.Logf("atomicWrite accepted the path (root-scoped ops may allow it)")
	} else {
		t.Logf("atomicWrite rejected: %v", werr)
	}

	if _, statErr := os.Stat(filepath.Join(outside, "pwned.txt")); statErr == nil {
		t.Fatal("SECURITY: write escaped the library root via symlink")
	}
}

// 绝对路径与 .. 穿越必须在 safePath 这一层就被拒，不依赖 os.Root 兜底。
func TestAtomicWriteRejectsTraversalForms(t *testing.T) {
	lib, err := OpenLibrary(t.TempDir())
	if err != nil {
		t.Fatalf("OpenLibrary: %v", err)
	}
	defer lib.Close()

	for _, name := range []string{
		"../escape.txt",
		"/tmp/absolute.txt",
		"a/../../b.txt",
		`windows\..\x.txt`,
		"nul\x00byte.txt",
	} {
		if werr := lib.atomicWrite(name, []byte("x")); werr == nil {
			t.Fatalf("atomicWrite accepted unsafe path %q", name)
		}
	}
}
