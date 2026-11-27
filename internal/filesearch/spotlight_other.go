//go:build !darwin

package filesearch

import (
	"context"
	"strings"
)

// SearchFiles supports pure-Go path-prefix listing on non-macOS hosts and
// returns nil for Spotlight metadata queries.
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
	return nil, nil
}
