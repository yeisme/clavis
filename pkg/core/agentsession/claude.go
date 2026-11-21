package agentsession

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func discoverClaude(ctx context.Context, opts Options) ([]Session, []Diagnostic) {
	entries, err := os.ReadDir(opts.ClaudeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, []Diagnostic{{"claude", "unavailable", "projects directory was not found"}}
		}
		return nil, []Diagnostic{{"claude", "read_failed", "projects directory is unreadable"}}
	}
	var out []Session
	var diags []Diagnostic
	now := opts.Now()
	for _, project := range entries {
		if err := ctx.Err(); err != nil {
			return out, append(diags, Diagnostic{"claude", "timeout", err.Error()})
		}
		if !project.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(opts.ClaudeDir, project.Name(), "*.jsonl"))
		for _, file := range files {
			s, ok := claudeFile(file, project.Name(), now)
			if ok {
				out = append(out, s)
			}
		}
	}
	return dedupe(out), diags
}

func claudeFile(file, workspace string, now time.Time) (Session, bool) {
	f, err := os.Open(file)
	if err != nil {
		return Session{}, false
	}
	defer f.Close()
	var id, title, message, state string
	var last time.Time
	pending := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		if id == "" {
			id = firstString(row, "sessionId", "session_id", "id")
		}
		if title == "" {
			title = firstString(row, "title", "slug")
		}
		if text := firstString(row, "content", "text", "message"); text != "" {
			message = text
		}
		if status := firstString(row, "status", "state"); status != "" {
			state = status
		}
		pending = pending || containsPending(row)
		if at := firstTime(row, "timestamp", "updatedAt", "createdAt"); !at.IsZero() && at.After(last) {
			last = at
		}
	}
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	if id == "" {
		return Session{}, false
	}
	return Session{ID: id, Provider: "claude", Title: title, Status: statusFromActivity(now, last, state, pending), LastActivityAt: last, LastMessage: sanitizeMessage(message), PendingReview: pending, Workspace: basename(workspace), Source: file, ObservedAt: now}, true
}
