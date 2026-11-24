package agentsession

import (
	"sort"
	"strings"
)

func mergeSessions(items []Session) []Session {
	if len(items) <= 1 {
		return items
	}
	// First pass: index non-herdr sessions by normalized ID so live Herdr
	// panes can enrich and supersede stale file snapshots without losing rich
	// local titles or last messages.
	herdrIdx := make(map[string]int)
	out := make([]Session, 0, len(items))
	for _, s := range items {
		if !s.HerdrManaged {
			continue
		}
		key := strings.TrimSpace(s.ID)
		if key != "" {
			herdrIdx[key] = len(out)
		}
		out = append(out, s)
	}
	for _, s := range items {
		if s.HerdrManaged {
			continue
		}
		id := strings.TrimSpace(s.ID)
		if idx, ok := herdrIdx[id]; ok {
			merged := out[idx]
			if merged.LastMessage == "" && s.LastMessage != "" {
				merged.LastMessage = s.LastMessage
			}
			if merged.ResultLine == "" && s.ResultLine != "" {
				merged.ResultLine = s.ResultLine
			}
			if merged.CurrentAction == "" && s.CurrentAction != "" {
				merged.CurrentAction = s.CurrentAction
			}
			if s.Title != "" && (merged.Title == "" || merged.Title == merged.AgentName || merged.Title == merged.Provider) {
				merged.Title = s.Title
			}
			if !s.LastActivityAt.IsZero() && s.LastActivityAt.After(merged.LastActivityAt) {
				merged.LastActivityAt = s.LastActivityAt
			}
			merged.PendingReview = merged.PendingReview || s.PendingReview
			merged.AwaitingHuman = merged.AwaitingHuman || s.AwaitingHuman
			out[idx] = merged
			continue
		}
		out = append(out, s)
	}
	return out
}

func sessionSortRank(s Session) int {
	if s.PendingReview || s.AwaitingHuman {
		return 0
	}
	if s.Status == StatusRunning {
		return 1
	}
	if s.Focused {
		return 2
	}
	if s.HerdrManaged && s.Status == StatusWaiting {
		return 3
	}
	if s.Status == StatusWaiting {
		return 4
	}
	return 5
}

func sortSessions(items []Session) []Session {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := sessionSortRank(items[i]), sessionSortRank(items[j])
		if ri != rj {
			return ri < rj
		}
		if !items[i].LastActivityAt.Equal(items[j].LastActivityAt) {
			return items[i].LastActivityAt.After(items[j].LastActivityAt)
		}
		if items[i].Provider != items[j].Provider {
			return items[i].Provider < items[j].Provider
		}
		if items[i].ID != items[j].ID {
			return items[i].ID < items[j].ID
		}
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items
}
