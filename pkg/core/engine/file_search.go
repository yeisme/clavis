package engine

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/i18n"
)

// FileMatch describes a local file or directory candidate surfaced in the Omnibox.
type FileMatch struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Dir         string `json:"dir,omitempty"`
	IsDir       bool   `json:"isDir,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	ModTimeUnix int64  `json:"modTimeUnix,omitempty"`
}

// ParseFileSearchQuery inspects a TabActions query for explicit file-search
// syntax ("f <q>", "file <q>", "~", "~/...", or multi-segment "/...") or
// implicit fallback eligibility (>= 2 runes without any quick-route prefix).
func ParseFileSearchQuery(raw string) (needle string, explicit bool, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, false
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "f "):
		return strings.TrimSpace(trimmed[2:]), true, true
	case strings.HasPrefix(lower, "file "):
		return strings.TrimSpace(trimmed[5:]), true, true
	case trimmed == "~" || strings.HasPrefix(trimmed, "~/"):
		return trimmed, true, true
	case len(trimmed) > 1 && trimmed[0] == '/' && trimmed[1] != '/' && strings.Contains(trimmed[1:], "/"):
		return trimmed, true, true
	}
	if _, _, routed := ParseOmniboxPrefix(trimmed); routed {
		return "", false, false
	}
	if utf8.RuneCountInString(trimmed) < 2 {
		return "", false, false
	}
	return trimmed, false, true
}

func makeFileOmniboxItems(matches []FileMatch) []OmniboxItem {
	items := make([]OmniboxItem, 0, len(matches))
	for _, m := range matches {
		mCopy := m
		icon := "doc"
		if m.IsDir {
			icon = "folder"
		}
		subtitle := m.Dir
		if subtitle == "" {
			subtitle = filepath.Dir(m.Path)
		}
		keyHint := i18n.T("engine.file.file")
		if m.IsDir {
			keyHint = i18n.T("engine.file.folder")
		}
		items = append(items, OmniboxItem{
			ID:       "file:" + m.Path,
			Title:    m.Name,
			Subtitle: subtitle,
			KeyHint:  keyHint,
			Icon:     icon,
			Keywords: []string{"file", m.Name, m.Path},
			Kind:     ItemFile,
			File:     &mCopy,
			Score:    500,
		})
	}
	return items
}
