package agentsession

import "sort"

func sortSessions(items []Session) []Session {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].LastActivityAt.Equal(items[j].LastActivityAt) {
			return items[i].Provider < items[j].Provider
		}
		return items[i].LastActivityAt.After(items[j].LastActivityAt)
	})
	return items
}
