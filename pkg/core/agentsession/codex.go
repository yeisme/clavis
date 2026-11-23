package agentsession

import (
	"context"
	"errors"
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
		code := codexDiagnosticCode(err)
		return nil, []Diagnostic{{"codex", code, "threads could not be read"}}
	}
	now := opts.Now()
	out := make([]Session, 0, len(threads))
	for _, thread := range threads {
		if strings.TrimSpace(thread.ID) == "" {
			continue
		}
		out = append(out, Session{
			ID: thread.ID, Provider: "codex", Title: thread.Title,
			Status:         statusFromActivity(now, thread.LastActivityAt, thread.Status, thread.PendingReview || thread.AwaitingHuman),
			LastActivityAt: thread.LastActivityAt,
			LastMessage:    sanitizeMessage(thread.LastMessage),
			PendingReview:  thread.PendingReview,
			CurrentAction:  sanitizeMessage(thread.CurrentAction),
			ResultLine:     sanitizeMessage(firstNonEmpty(thread.ResultLine, thread.LastMessage)),
			AwaitingHuman:  thread.AwaitingHuman || thread.PendingReview,
			Workspace:      basename(thread.Workspace), Source: opts.CodexDB, ObservedAt: now,
		})
	}
	return out, nil
}

func codexDiagnosticCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "cancel") {
		return "cancelled"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "locked"), strings.Contains(message, "busy"):
		return "busy"
	case strings.Contains(message, "schema"), strings.Contains(message, "table not found"), strings.Contains(message, "no stable id"), strings.Contains(message, "no such column"):
		return "incompatible_schema"
	case strings.Contains(message, "open read-only"), strings.Contains(message, "not found"), strings.Contains(message, "permission"):
		return "unavailable"
	default:
		return "read_failed"
	}
}
