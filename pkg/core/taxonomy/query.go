package taxonomy

import "strings"

// FilterQuery represents a unified Omnibox & Settings filter query parsed from
// free text, #tag inclusions, -#tag exclusions, is:fav / is:favorite, and
// controlled facets (provider:, kind:, status:, rights:, env:, locale:).
type FilterQuery struct {
	Raw          string            `json:"raw,omitempty"`
	FreeText     string            `json:"freeText,omitempty"`
	IncludeTags  []string          `json:"includeTags,omitempty"`
	ExcludeTags  []string          `json:"excludeTags,omitempty"`
	FavoriteOnly bool              `json:"favoriteOnly,omitempty"`
	Facets       map[string]string `json:"facets,omitempty"`
}

var allowedFacets = map[string]bool{
	"provider": true,
	"kind":     true,
	"status":   true,
	"rights":   true,
	"env":      true,
	"locale":   true,
}

// ParseFilterQuery splits raw into structured filter predicates and remaining
// free-text terms.
func ParseFilterQuery(raw string) FilterQuery {
	q := FilterQuery{Raw: raw}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return q
	}

	var (
		freeParts   []string
		seenInclude map[string]struct{}
		seenExclude map[string]struct{}
	)

	for _, tok := range fields {
		if strings.EqualFold(tok, "is:fav") || strings.EqualFold(tok, "is:favorite") {
			q.FavoriteOnly = true
			continue
		}
		if strings.HasPrefix(tok, "-#") {
			tag := NormalizeTag(tok[2:])
			if tag != "" {
				if seenExclude == nil {
					seenExclude = make(map[string]struct{})
				}
				if _, dup := seenExclude[tag]; !dup {
					seenExclude[tag] = struct{}{}
					q.ExcludeTags = append(q.ExcludeTags, tag)
				}
				continue
			}
		}
		if strings.HasPrefix(tok, "#") {
			tag := NormalizeTag(tok[1:])
			if tag != "" {
				if seenInclude == nil {
					seenInclude = make(map[string]struct{})
				}
				if _, dup := seenInclude[tag]; !dup {
					seenInclude[tag] = struct{}{}
					q.IncludeTags = append(q.IncludeTags, tag)
				}
				continue
			}
		}
		if idx := strings.IndexByte(tok, ':'); idx > 0 {
			facet := toLowerASCII(strings.TrimSpace(tok[:idx]))
			val := toLowerASCII(strings.TrimSpace(tok[idx+1:]))
			if allowedFacets[facet] && val != "" {
				if q.Facets == nil {
					q.Facets = make(map[string]string)
				}
				q.Facets[facet] = val
				continue
			}
		}
		freeParts = append(freeParts, tok)
	}

	q.FreeText = strings.Join(freeParts, " ")
	return q
}

// IsEmpty reports whether q contains no active filter constraints.
func (q FilterQuery) IsEmpty() bool {
	return strings.TrimSpace(q.FreeText) == "" &&
		len(q.IncludeTags) == 0 &&
		len(q.ExcludeTags) == 0 &&
		!q.FavoriteOnly &&
		len(q.Facets) == 0
}

// MatchesTags reports whether tags satisfy q's IncludeTags and ExcludeTags.
func (q FilterQuery) MatchesTags(tags []string) bool {
	if len(q.IncludeTags) == 0 && len(q.ExcludeTags) == 0 {
		return true
	}
	tagSet := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		if norm := NormalizeTag(t); norm != "" {
			tagSet[norm] = struct{}{}
		}
	}
	for _, inc := range q.IncludeTags {
		norm := NormalizeTag(inc)
		if norm == "" {
			continue
		}
		if _, ok := tagSet[norm]; !ok {
			return false
		}
	}
	for _, exc := range q.ExcludeTags {
		norm := NormalizeTag(exc)
		if norm == "" {
			continue
		}
		if _, ok := tagSet[norm]; ok {
			return false
		}
	}
	return true
}

// Matches reports whether an entity's searchable text, tags, favorite flag, and
// facet map satisfy all predicates in q.
func (q FilterQuery) Matches(text string, tags []string, favorite bool, facets map[string]string) bool {
	if q.FavoriteOnly && !favorite {
		return false
	}
	if !q.MatchesTags(tags) {
		return false
	}
	for k, want := range q.Facets {
		wantNorm := toLowerASCII(strings.TrimSpace(want))
		if wantNorm == "" {
			continue
		}
		got, ok := lookupFacet(facets, k)
		if !ok || toLowerASCII(strings.TrimSpace(got)) != wantNorm {
			return false
		}
	}
	if strings.TrimSpace(q.FreeText) != "" {
		lowerText := strings.ToLower(text)
		for _, term := range strings.Fields(strings.ToLower(q.FreeText)) {
			if !strings.Contains(lowerText, term) {
				return false
			}
		}
	}
	return true
}

func lookupFacet(facets map[string]string, key string) (string, bool) {
	if len(facets) == 0 {
		return "", false
	}
	if v, ok := facets[key]; ok {
		return v, true
	}
	for k, v := range facets {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}
