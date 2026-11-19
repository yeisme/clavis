package ime

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func CompileRimeDict(r io.Reader) (*Lexicon, error) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var items []lexItem
	decidedHeader := false
	inHeader := false
	firstLine := true
	for scan.Scan() {
		rawLine := scan.Text()
		if firstLine {
			rawLine = strings.TrimPrefix(rawLine, "\uFEFF")
			firstLine = false
		}
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !decidedHeader {
			decidedHeader = true
			if line == "---" || (!strings.ContainsRune(line, '\t') && strings.ContainsRune(line, ':')) {
				inHeader = true
				continue
			}
		}
		if inHeader {
			if line == "..." {
				inHeader = false
			}
			continue
		}
		if line == "..." {
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		word := strings.TrimSpace(fields[0])
		if word == "" {
			continue
		}
		second := strings.TrimSpace(fields[1])
		py, freq := second, int32(0)
		if n, ok := parseRimeWeight(second); ok {
			freq = n
			py = compactPinyin(word)
		} else if len(fields) > 2 {
			py = second
			if n, ok := parseRimeWeight(fields[2]); ok {
				freq = n
			}
		}
		py = normalizeRimePinyin(py)
		if py == "" {
			continue
		}
		items = append(items, lexItem{py, word, freq})
	}
	if e := scan.Err(); e != nil {
		return nil, e
	}
	if len(items) == 0 {
		return nil, errors.New("ime: no dictionary entries")
	}
	return buildLexicon(items), nil
}

func parseRimeWeight(s string) (int32, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 32); err == nil {
		return int32(n), true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int32(f * 100), true
	}
	return 0, false
}

func normalizeRimePinyin(py string) string {
	py = strings.TrimSpace(strings.ReplaceAll(strings.ToLower(py), "ü", "v"))
	if py == "" {
		return ""
	}
	var parts []string
	for _, token := range strings.Fields(py) {
		token = strings.ReplaceAll(token, "'", "")
		if token == "" {
			continue
		}
		parts = append(parts, splitQuanpin(token)...)
	}
	return strings.Join(parts, " ")
}

func compactPinyin(word string) string { // reasonable fallback for text<TAB>weight entries
	var b strings.Builder
	for _, r := range word {
		if r < 128 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func CompileRimeDictFiles(paths []string, out string) (*Lexicon, error) {
	if len(paths) == 0 {
		return nil, errors.New("ime: no dictionary paths")
	}
	var all []lexItem
	for _, p := range paths {
		f, e := os.Open(p)
		if e != nil {
			return nil, e
		}
		var magic [4]byte
		n, _ := io.ReadFull(f, magic[:])
		if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
			f.Close()
			return nil, seekErr
		}
		var l *Lexicon
		if n == 4 && bytes.Equal(magic[:], clvMagic[:]) {
			data, readErr := io.ReadAll(f)
			f.Close()
			if readErr != nil {
				return nil, readErr
			}
			l, e = LoadClvDict(data)
		} else {
			l, e = CompileRimeDict(f)
			f.Close()
		}
		if e != nil {
			return nil, e
		}
		for _, w := range l.words {
			all = append(all, lexItem{l.pinyin(w), l.text(w), w.freq})
		}
	}
	if len(all) == 0 {
		return nil, errors.New("ime: no dictionary entries")
	}
	l := buildLexicon(all)
	if out != "" {
		if dir := filepath.Dir(out); dir != "" && dir != "." {
			if e := os.MkdirAll(dir, 0755); e != nil {
				return nil, e
			}
		}
		if e := os.WriteFile(out, l.image, 0644); e != nil {
			return nil, e
		}
	}
	return l, nil
}

// dictCompilerVersion is part of the cache key. Bump it whenever the compiled .clvdict
// layout or the parsing rules change: without it, a dictionary compiled by an older
// compiler keeps being served from ~/.cache after the compiler is fixed, and the stale
// entries look like a parsing bug that cannot be reproduced.
const dictCompilerVersion = 2

func LoadOrCompileDicts(paths []string, cacheDir string) (*Lexicon, error) {
	if len(paths) == 0 {
		return StarterLexicon(), nil
	}
	h := sha256.New()
	fmt.Fprintf(h, "compiler=%d\x00", dictCompilerVersion)
	var numBuf [16]byte
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			return nil, errors.New("ime: dictionary path is a directory")
		}
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
		binary.LittleEndian.PutUint64(numBuf[:8], uint64(st.Size()))
		binary.LittleEndian.PutUint64(numBuf[8:], uint64(st.ModTime().UnixNano()))
		_, _ = h.Write(numBuf[:])
	}
	cache := ""
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return nil, err
		}
		sum := hex.EncodeToString(h.Sum(nil)[:8])
		cache = filepath.Join(cacheDir, "clavis-"+sum+".clvdict")
		if d, e := os.ReadFile(cache); e == nil {
			if l, e := LoadClvDict(d); e == nil {
				return l, nil
			}
		}
	}
	return CompileRimeDictFiles(paths, cache)
}
