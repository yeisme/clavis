package agentsession

import (
	"context"
	"strings"
)

func discoverCodex(ctx context.Context, opts Options) ([]Session, []Diagnostic) {
	if strings.Contains(opts.CodexDB, "immutable=1") {
		return nil, []Diagnostic{{"codex", "unsafe_read_mode", "immutable=1 is forbidden; use mode=ro"}}
	}
	if opts.CodexReader == nil {
		return nil, []Diagnostic{{"codex", "no_reader_configured", "Codex state reader is not configured"}}
	}
	threads, err := opts.CodexReader.Threads(ctx, 500)
	if err != nil {
		return nil, []Diagnostic{{"codex", "query_failed", "threads could not be read"}}
	}
	now := opts.Now()
	out := make([]Session, 0, len(threads))
	for _, thread := range threads {
		if strings.TrimSpace(thread.ID) == "" {
			continue
		}
		out = append(out, Session{
			ID: thread.ID, Provider: "codex", Title: thread.Title,
			Status:         statusFromActivity(now, thread.LastActivityAt, thread.Status, thread.PendingReview),
			LastActivityAt: thread.LastActivityAt,
			LastMessage:    sanitizeMessage(thread.LastMessage),
			PendingReview:  thread.PendingReview,
			Workspace:      basename(thread.Workspace), Source: opts.CodexDB, ObservedAt: now,
		})
	}
	return out, nil
}
