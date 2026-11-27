package filesearch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultLimit   = 20
	maxLimit       = 50
	defaultTimeout = 350 * time.Millisecond
)

// FileMatch represents a local file or directory candidate returned by search.
type FileMatch struct {
	Path      string
	Name      string
	Dir       string
	IsDir     bool
	SizeBytes int64
	ModTime   time.Time
}

// Options controls Spotlight and filesystem path-prefix searches.
type Options struct {
	ScopeDir    string
	Limit       int
	Timeout     time.Duration
	ExcludeDirs []string
}

// DefaultExcludeDirs returns directory names or relative subpaths excluded by default.
func DefaultExcludeDirs() []string {
	return []string{".git", "node_modules", "Library/Caches", ".Trash", ".venv", "__pycache__"}
}

func normalizeOptions(opts Options) Options {
	if strings.TrimSpace(opts.ScopeDir) == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			opts.ScopeDir = home
		}
	}
	if opts.ScopeDir != "" {
		opts.ScopeDir = filepath.Clean(opts.ScopeDir)
	}
	if opts.Limit <= 0 {
		opts.Limit = defaultLimit
	} else if opts.Limit > maxLimit {
		opts.Limit = maxLimit
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if len(opts.ExcludeDirs) == 0 {
		opts.ExcludeDirs = DefaultExcludeDirs()
	}
	return opts
}

// EscapeMDQueryLiteral strips ASCII control characters and escapes Spotlight predicate
// metacharacters so untrusted user input cannot inject mdfind query clauses.
func EscapeMDQueryLiteral(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		switch r {
		case '\\', '"', '\'', '*', '?':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// BuildMDQuery builds a case- and diacritic-insensitive kMDItemDisplayName predicate.
func BuildMDQuery(needle string) string {
	return fmt.Sprintf(`kMDItemDisplayName == "*%s*"cdw`, EscapeMDQueryLiteral(needle))
}

func isPathPrefixQuery(q string) bool {
	trimmed := strings.TrimSpace(q)
	return trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "/")
}

func isExcludedPath(path, scopeDir string, excludes []string) bool {
	cleanPath := filepath.Clean(path)
	slashPath := filepath.ToSlash(cleanPath)

	relSlash := ""
	if scopeDir != "" {
		if rel, err := filepath.Rel(filepath.Clean(scopeDir), cleanPath); err == nil &&
			rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			relSlash = filepath.ToSlash(rel)
		}
	}

	for _, raw := range excludes {
		ex := strings.Trim(filepath.ToSlash(filepath.Clean(raw)), "/")
		if ex == "" || ex == "." {
			continue
		}
		if strings.Contains(ex, "/") {
			if relSlash == ex || strings.HasPrefix(relSlash, ex+"/") ||
				strings.HasSuffix(slashPath, "/"+ex) || strings.Contains(slashPath, "/"+ex+"/") {
				return true
			}
			continue
		}
		checkTarget := slashPath
		if relSlash != "" {
			checkTarget = relSlash
		}
		for _, seg := range strings.Split(checkTarget, "/") {
			if seg == ex {
				return true
			}
		}
	}
	return false
}

func listPathPrefix(ctx context.Context, rawQuery string, opts Options) ([]FileMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(rawQuery)
	home := opts.ScopeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	var dir, prefix string
	switch {
	case q == "~" || q == "~/":
		dir = home
		prefix = ""
	case strings.HasPrefix(q, "~/"):
		rest := strings.TrimPrefix(q, "~/")
		if strings.HasSuffix(rest, "/") {
			dir = filepath.Join(home, rest)
			prefix = ""
		} else if idx := strings.LastIndex(rest, "/"); idx < 0 {
			dir = home
			prefix = rest
		} else {
			sub := rest[:idx]
			if sub == "" {
				dir = home
			} else {
				dir = filepath.Join(home, sub)
			}
			prefix = rest[idx+1:]
		}
	case q == "/":
		dir = string(filepath.Separator)
		prefix = ""
	case strings.HasPrefix(q, "/"):
		if strings.HasSuffix(q, "/") {
			dir = filepath.Clean(q)
			prefix = ""
		} else {
			idx := strings.LastIndex(q, "/")
			dir = filepath.Clean(q[:idx+1])
			prefix = q[idx+1:]
		}
	default:
		return nil, nil
	}
	if dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			return nil, nil
		}
		return nil, err
	}

	includeHidden := strings.HasPrefix(prefix, ".")
	lowerPrefix := strings.ToLower(prefix)
	matches := make([]FileMatch, 0, min(len(entries), opts.Limit))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if !includeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if lowerPrefix != "" && !strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if isExcludedPath(fullPath, home, opts.ExcludeDirs) {
			continue
		}
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			continue
		}
		matches = append(matches, FileMatch{
			Path:      fullPath,
			Name:      name,
			Dir:       dir,
			IsDir:     info.IsDir(),
			SizeBytes: info.Size(),
			ModTime:   info.ModTime(),
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].IsDir != matches[j].IsDir {
			return matches[i].IsDir
		}
		li, lj := strings.ToLower(matches[i].Name), strings.ToLower(matches[j].Name)
		if li != lj {
			return li < lj
		}
		return matches[i].Path < matches[j].Path
	})
	if len(matches) > opts.Limit {
		matches = matches[:opts.Limit]
	}
	return matches, nil
}

func rankFileMatches(matches []FileMatch, needle string, limit int) []FileMatch {
	lowerNeedle := strings.ToLower(strings.TrimSpace(needle))
	tier := func(name string) int {
		ln := strings.ToLower(name)
		switch {
		case lowerNeedle != "" && ln == lowerNeedle:
			return 0
		case lowerNeedle != "" && strings.HasPrefix(ln, lowerNeedle):
			return 1
		case lowerNeedle != "" && strings.Contains(ln, lowerNeedle):
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		ti, tj := tier(matches[i].Name), tier(matches[j].Name)
		if ti != tj {
			return ti < tj
		}
		if !matches[i].ModTime.Equal(matches[j].ModTime) {
			return matches[i].ModTime.After(matches[j].ModTime)
		}
		return matches[i].Path < matches[j].Path
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}
