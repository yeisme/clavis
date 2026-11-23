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
		files, globErr := filepath.Glob(filepath.Join(opts.ClaudeDir, project.Name(), "*.jsonl"))
		if globErr != nil {
			diags = append(diags, Diagnostic{"claude", "glob_failed", "project session files could not be enumerated"})
			continue
		}
		for _, file := range files {
			s, ok, diag := claudeFileWithDiagnostic(file, project.Name(), now)
			if diag != nil {
				diags = append(diags, *diag)
			}
			if ok {
				out = append(out, s)
			}
		}
	}
	return dedupe(out), diags
}

func claudeFile(file, workspace string, now time.Time) (Session, bool) {
	session, ok, _ := claudeFileWithDiagnostic(file, workspace, now)
	return session, ok
}

func claudeFileWithDiagnostic(file, workspace string, now time.Time) (Session, bool, *Diagnostic) {
	f, err := os.Open(file)
	if err != nil {
		return Session{}, false, &Diagnostic{"claude", "read_failed", "session log unreadable"}
	}
	defer f.Close()
	var id, title, message, currentAction, resultLine, state string
	var last time.Time
	var pending, awaitingHuman bool
	invalidJSON := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			invalidJSON = true
			continue
		}
		compat := compatibilityFromMap(row)
		if compat.currentAction != "" {
			currentAction = compat.currentAction
		}
		if compat.resultLine != "" {
			resultLine = compat.resultLine
		}
		awaitingHuman = awaitingHuman || compat.awaitingHuman
		pending = pending || compat.pendingReview
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
		if at := firstTime(row, "timestamp", "updatedAt", "createdAt"); !at.IsZero() && at.After(last) {
			last = at
		}
	}
	if err := sc.Err(); err != nil {
		return Session{}, false, &Diagnostic{"claude", "read_failed", "session log could not be read"}
	}
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	if id == "" {
		return Session{}, false, nil
	}
	message = firstNonEmpty(resultLine, message)
	var diagnostic *Diagnostic
	if invalidJSON {
		diagnostic = &Diagnostic{"claude", "invalid_json", "session log contains invalid entries"}
	}
	return Session{ID: id, Provider: "claude", Title: title, Status: statusFromActivity(now, last, state, pending || awaitingHuman), LastActivityAt: last, LastMessage: sanitizeMessage(message), PendingReview: pending, CurrentAction: sanitizeMessage(currentAction), ResultLine: sanitizeMessage(message), AwaitingHuman: awaitingHuman, Workspace: basename(workspace), Source: file, ObservedAt: now}, true, diagnostic
}
