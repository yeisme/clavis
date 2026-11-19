package template

import "sort"

// Selection is one canonical template per eligible solution. Explicit installs
// may select another variant using Library.Import; those are independent pins
// with stable solution.role.locale IDs, not duplicate discovery search hits.
type Selection struct {
	Solution Solution
	Template Template
}

// DefaultSelections prefers main role, then requested/default/en locale, with
// lexical fallback for real solutions that have no main role. Sorting a copied
// template slice makes selection independent of catalog/discovery row order.
func DefaultSelections(c Catalog, requestedLocale string) []Selection {
	var out []Selection
	for _, s := range DefaultSolutions(c) {
		templates := append([]Template(nil), s.Templates...)
		localeRank := func(locale string) int {
			for i, want := range []string{requestedLocale, c.Repository.DefaultLocale, "en"} {
				if want != "" && locale == want {
					return i
				}
			}
			return 3
		}
		sort.Slice(templates, func(i, j int) bool {
			a, b := templates[i], templates[j]
			if (a.Role == "main") != (b.Role == "main") {
				return a.Role == "main"
			}
			if localeRank(a.Locale) != localeRank(b.Locale) {
				return localeRank(a.Locale) < localeRank(b.Locale)
			}
			if a.Role != b.Role {
				return a.Role < b.Role
			}
			if a.Locale != b.Locale {
				return a.Locale < b.Locale
			}
			return a.Path < b.Path
		})
		if len(templates) > 0 {
			out = append(out, Selection{Solution: s, Template: templates[0]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Solution.Key() < out[j].Solution.Key() })
	return out
}
