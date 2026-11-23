package agentsession

import (
	"sort"
	"strings"
)

func sortSessions(items []Session) []Session {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].LastActivityAt.Equal(items[j].LastActivityAt) {
			if items[i].Provider != items[j].Provider {
				return items[i].Provider < items[j].Provider
			}
			if items[i].ID != items[j].ID {
				return items[i].ID < items[j].ID
			}
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		}
		return items[i].LastActivityAt.After(items[j].LastActivityAt)
	})
	return items
}
