package ime

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func CompileRimeDict(r io.Reader) (*Lexicon, error) {
	scan := bufio.NewScanner(r)
	var items []lexItem
	header := true
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if header {
			if line == "..." {
				header = false
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
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
		if n, e := strconv.ParseInt(second, 10, 32); e == nil {
			freq = int32(n)
			py = compactPinyin(word)
		} else if len(fields) > 2 {
			py = second
			n, _ := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 32)
			freq = int32(n)
		}
		if py == "" {
			continue
		}
		py = strings.Join(splitQuanpin(strings.ReplaceAll(strings.ToLower(py), "'", "")), " ")
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
	var all []lexItem
	for _, p := range paths {
		f, e := os.Open(p)
		if e != nil {
			return nil, e
		}
		l, e := CompileRimeDict(f)
		f.Close()
		if e != nil {
			return nil, e
		}
		for _, w := range l.words {
			all = append(all, lexItem{l.pinyin(w), l.text(w), w.freq})
		}
	}
	l := buildLexicon(all)
	if out != "" {
		if e := os.WriteFile(out, l.image, 0644); e != nil {
			return nil, e
		}
	}
	return l, nil
}
func LoadOrCompileDicts(paths []string, cacheDir string) (*Lexicon, error) {
	if len(paths) == 0 {
		return StarterLexicon(), nil
	}
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return nil, err
		}
	}
	cache := filepath.Join(cacheDir, "clavis.clvdict")
	if d, e := os.ReadFile(cache); e == nil {
		if l, e := LoadClvDict(d); e == nil {
			return l, nil
		}
	}
	return CompileRimeDictFiles(paths, cache)
}
