package clipboard

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// InlineTextMaxBytes bounds how many UTF-8 bytes of a single text entry are
	// kept inline in SQLite for fast summary queries and FTS indexing.
	InlineTextMaxBytes = 64 << 10
	// MaxOverflowTextReadBytes caps gunzip decompression to prevent bombs on corrupt blobs.
	MaxOverflowTextReadBytes = 32 << 20
	textOverflowDirName      = "text"
)

var (
	// ErrOverflowTextTooLarge indicates a decompressed overflow text blob exceeds safety bounds.
	ErrOverflowTextTooLarge = errors.New("clipboard: overflow text exceeds size limit")
	// ErrOverflowTextMismatch indicates stored overflow text failed SHA-256 verification.
	ErrOverflowTextMismatch = errors.New("clipboard: overflow text checksum mismatch")
)

// TruncateUTF8Bytes returns a valid UTF-8 prefix of s whose byte length is at most
// maxBytes, along with a boolean reporting whether truncation occurred.
func TruncateUTF8Bytes(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", len(s) > 0
	}
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	if cut == 0 {
		return "", len(s) > 0
	}
	if !utf8.ValidString(s[:cut]) {
		// Step back over an incomplete trailing rune when cut landed mid-sequence.
		for cut > 0 && !utf8.ValidString(s[:cut]) {
			cut--
		}
	}
	return s[:cut], true
}

func (m *MediaStore) textOverflowDir() string {
	return filepath.Join(m.Dir, textOverflowDirName)
}

func (m *MediaStore) textOverflowPath(sha string) string {
	return filepath.Join(m.textOverflowDir(), sha+".txt.gz")
}

// SaveOverflowText writes the full UTF-8 content compressed with gzip under
// <MediaDir>/text/<sha256>.txt.gz atomically (0o600) and returns the slash-normalized
// relative path plus raw content SHA-256 hex digest.
func (m *MediaStore) SaveOverflowText(content string) (relPath string, sha string, err error) {
	if m == nil || strings.TrimSpace(m.Dir) == "" {
		return "", "", errors.New("clipboard: media store dir is empty")
	}
	raw := []byte(content)
	sum := sha256.Sum256(raw)
	sha = hex.EncodeToString(sum[:])

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return "", "", err
	}
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return "", "", err
	}
	if err := gz.Close(); err != nil {
		return "", "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dst := m.textOverflowPath(sha)
	if err := writeFileAtomic(dst, buf.Bytes()); err != nil {
		return "", "", err
	}
	_ = os.Chmod(dst, 0o600)
	return textOverflowDirName + "/" + sha + ".txt.gz", sha, nil
}

// LoadOverflowText reads and verifies a compressed overflow text blob written by SaveOverflowText.
func (m *MediaStore) LoadOverflowText(relPath string, expectedSHA string) (string, error) {
	if m == nil || strings.TrimSpace(m.Dir) == "" {
		return "", errors.New("clipboard: media store dir is empty")
	}
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relPath)))
	if cleaned == "" || cleaned == "." || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("clipboard: invalid overflow relative path %q", relPath)
	}
	baseDir := filepath.Clean(m.textOverflowDir())
	fullPath := filepath.Clean(filepath.Join(m.Dir, cleaned))
	relToText, err := filepath.Rel(baseDir, fullPath)
	if err != nil || relToText == "." || strings.HasPrefix(relToText, "..") {
		return "", fmt.Errorf("clipboard: overflow path %q escapes text directory", relPath)
	}
	info, err := os.Lstat(fullPath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("clipboard: overflow path %q is a symlink", fullPath)
	}
	if info.IsDir() {
		return "", fmt.Errorf("clipboard: overflow path %q is a directory", fullPath)
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("clipboard: open gzip overflow: %w", err)
	}
	defer gz.Close()

	raw, err := io.ReadAll(io.LimitReader(gz, MaxOverflowTextReadBytes+1))
	if err != nil {
		return "", fmt.Errorf("clipboard: read gzip overflow: %w", err)
	}
	if len(raw) > MaxOverflowTextReadBytes {
		return "", ErrOverflowTextTooLarge
	}

	if expectedSHA != "" {
		sum := sha256.Sum256(raw)
		actual := hex.EncodeToString(sum[:])
		if actual != expectedSHA {
			return "", fmt.Errorf("%w: got %s want %s", ErrOverflowTextMismatch, actual, expectedSHA)
		}
	}
	return string(raw), nil
}
