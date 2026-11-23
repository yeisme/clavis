package agentsession

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func discoverDSH(ctx context.Context, opts Options) ([]Session, []Diagnostic) {
	files, err := filepath.Glob(filepath.Join(opts.DSHDir, "*.json"))
	if err != nil {
		return nil, []Diagnostic{{"dsh", "glob_failed", "cannot enumerate session cache"}}
	}
	var out []Session
	var diags []Diagnostic
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return out, append(diags, Diagnostic{"dsh", "timeout", err.Error()})
		}
		data, err := os.ReadFile(file)
		if err != nil {
			diags = append(diags, Diagnostic{"dsh", "read_failed", "session cache unreadable"})
			continue
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) != nil {
			diags = append(diags, Diagnostic{"dsh", "invalid_json", "session cache entry is invalid"})
			continue
		}
		s := dshSession(raw, file, opts.Now())
		if s.ID != "" {
			out = append(out, s)
		}
	}
	// JSONL threads supplement the cache when a cache entry is absent.
	threadFiles, _ := filepath.Glob(filepath.Join(opts.DSHThreadsDir, "*.jsonl"))
	for _, file := range threadFiles {
		if err := ctx.Err(); err != nil {
			return out, append(diags, Diagnostic{"dsh", "timeout", err.Error()})
		}
		s, ok, diag := dshThread(file, opts.Now())
		if diag != nil {
			diags = append(diags, *diag)
		}
		if ok {
			out = append(out, s)
		}
	}
	return dedupe(out), diags
}

func dshSession(raw map[string]any, source string, now time.Time) Session {
	identity := mapValue(raw, "record", "identity")
	rows := mapValue(raw, "record", "rows")
	id := firstString(identity, "id", "sessionId", "session_id")
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	at := firstTime(identity, "updatedAt", "lastActivityAt", "createdAt")
	if at.IsZero() {
		at = firstTime(rows, "updatedAt", "lastActivityAt")
	}
	title := firstString(rows, "title")
	workspace := firstString(identity, "cwd", "workingDirectory")
	if workspace == "" {
		workspace = firstString(rows, "workingDirectory", "cwd")
	}
	message := firstString(rows, "lastMessage", "summary")
	compat := compatibilityFromMap(rows)
	message = firstNonEmpty(compat.resultLine, message)
	return Session{ID: id, Provider: "dsh", Title: title, Status: statusFromActivity(now, at, firstString(rows, "status", "state"), compat.pendingReview || compat.awaitingHuman), LastActivityAt: at, LastMessage: sanitizeMessage(message), PendingReview: compat.pendingReview, CurrentAction: sanitizeMessage(compat.currentAction), ResultLine: sanitizeMessage(message), AwaitingHuman: compat.awaitingHuman, Workspace: basename(workspace), TargetRef: firstString(identity, "targetRef", "pane", "sessionId"), Source: source, ObservedAt: now}
}

func dshThread(file string, now time.Time) (Session, bool, *Diagnostic) {
	f, err := os.Open(file)
	if err != nil {
		return Session{}, false, &Diagnostic{"dsh", "read_failed", "thread log unreadable"}
	}
	defer f.Close()
	var id, cwd, title, message, currentAction, resultLine, explicitStatus string
	var awaitingHuman, pendingReview bool
	invalidJSON := false
	var last time.Time
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			invalidJSON = true
			continue
		}
		typ := strings.ToLower(firstString(row, "type"))
		if typ == "session" {
			id = firstString(row, "id", "session_id")
			cwd = firstString(row, "cwd", "workingDirectory")
			title = firstString(row, "title")
		}
		compat := compatibilityFromMap(row)
		if compat.currentAction != "" {
			currentAction = compat.currentAction
		}
		if compat.resultLine != "" {
			resultLine = compat.resultLine
		}
		awaitingHuman = awaitingHuman || compat.awaitingHuman
		pendingReview = pendingReview || compat.pendingReview
		if status := firstString(row, "status", "state"); status != "" {
			explicitStatus = status
		}
		at := firstTime(row, "timestamp", "updatedAt", "createdAt")
		if !at.IsZero() && at.After(last) {
			last = at
		}
		if typ == "message" {
			if text := firstString(row, "text", "content", "message"); text != "" {
				message = text
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Session{}, false, &Diagnostic{"dsh", "read_failed", "thread log unreadable"}
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
		diagnostic = &Diagnostic{"dsh", "invalid_json", "thread log contains invalid entries"}
	}
	return Session{ID: id, Provider: "dsh", Title: title, Status: statusFromActivity(now, last, explicitStatus, pendingReview || awaitingHuman), LastActivityAt: last, LastMessage: sanitizeMessage(message), PendingReview: pendingReview, CurrentAction: sanitizeMessage(currentAction), ResultLine: sanitizeMessage(message), AwaitingHuman: awaitingHuman, Workspace: basename(cwd), Source: file, ObservedAt: now}, true, diagnostic
}

func mapValue(root map[string]any, path ...string) map[string]any {
	cur := root
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}
func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func firstTime(m map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		if v := firstString(m, key); v != "" {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
func dedupe(items []Session) []Session {
	seen := map[string]bool{}
	out := items[:0]
	for _, s := range items {
		key := s.Provider + "\x00" + s.ID
		if !seen[key] {
			seen[key] = true
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActivityAt.After(out[j].LastActivityAt) })
	return out
}
