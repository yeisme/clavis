//go:build darwin

package filesearch

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var mdfindRunner = runMDFindExec

func runMDFindExec(ctx context.Context, scopeDir, predicate string) ([]string, error) {
	args := make([]string, 0, 3)
	if scopeDir != "" {
		args = append(args, "-onlyin", scopeDir)
	}
	args = append(args, predicate)
	cmd := exec.CommandContext(ctx, "/usr/bin/mdfind", args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	rawLines := bytes.Split(out, []byte{'\n'})
	paths := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		p := strings.TrimSpace(string(line))
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// SearchFiles searches local files and directories using path-prefix expansion
// for ~ / / queries, or bounded /usr/bin/mdfind queries on macOS.
func SearchFiles(ctx context.Context, query string, opts Options) ([]FileMatch, error) {
	opts = normalizeOptions(opts)
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	searchCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	if isPathPrefixQuery(trimmed) {
		return listPathPrefix(searchCtx, trimmed, opts)
	}
	if strings.TrimSpace(EscapeMDQueryLiteral(trimmed)) == "" {
		return nil, nil
	}

	predicate := BuildMDQuery(trimmed)
	rawPaths, err := mdfindRunner(searchCtx, opts.ScopeDir, predicate)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(rawPaths))
	matches := make([]FileMatch, 0, min(len(rawPaths), opts.Limit))
	for _, rawPath := range rawPaths {
		if err := searchCtx.Err(); err != nil {
			return nil, err
		}
		p := filepath.Clean(rawPath)
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		if isExcludedPath(p, opts.ScopeDir, opts.ExcludeDirs) {
			continue
		}
		info, statErr := os.Stat(p)
		if statErr != nil {
			continue
		}
		matches = append(matches, FileMatch{
			Path:      p,
			Name:      filepath.Base(p),
			Dir:       filepath.Dir(p),
			IsDir:     info.IsDir(),
			SizeBytes: info.Size(),
			ModTime:   info.ModTime(),
		})
	}
	return rankFileMatches(matches, trimmed, opts.Limit), nil
}
