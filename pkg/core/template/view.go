package template

import (
	"sort"
	"strings"
)

// Source is the merged-view classification; SourceKind retains the import origin.
type Source string

const (
	Local    Source = "local"
	Imported Source = "imported"
	Bundled  Source = "bundled"
)

type SourceKind string

const (
	SourceLocal      SourceKind = "local"
	SourcePromptrepo SourceKind = "promptrepo"
	SourceFile       SourceKind = "file"
	SourceBundled    SourceKind = "bundled"
	SourceGit        SourceKind = "git"
	SourceRegistry   SourceKind = "registry"
)

// SlotDefault is intentionally not a contract input. Contract interpretation is
// owned by the renderer; this layer carries only user-authored defaults.
type SlotDefault struct {
	Default *string `toml:"default" json:"default,omitempty"`
}

// Item is a detached view. Body is available in memory but never serialized.
// ContractDigest addresses raw sidecar bytes, NOT its declared canonical digest.
type Item struct {
	ID           string                 `json:"id"`
	Trigger      string                 `json:"trigger"`
	Title        string                 `json:"title"`
	Category     string                 `json:"category"`
	Description  string                 `json:"description,omitempty"`
	Body         string                 `json:"-"`
	AppBundleIDs []string               `json:"app_bundle_ids,omitempty"`
	SlotDefaults map[string]SlotDefault `json:"-"`
	Enabled      bool                   `json:"enabled"`
	Source       Source                 `json:"source"`
	SourceKind   SourceKind             `json:"sourceKind"`
	Maturity     string                 `json:"maturity,omitempty"`
	Provenance   *ImportRecord          `json:"provenance,omitempty"`
	Orphaned     bool                   `json:"orphaned"`
	UpdateStatus string                 `json:"updateStatus"`
	// ContractStatus reports full contract validation status:
	// "validated", "header_only_unvalidated", "contract_invalid", or "degraded_no_contract".
	ContractStatus      string       `json:"contractStatus"`
	Contract            *Contract    `json:"-"`
	ContractDiagnostics []Diagnostic `json:"contractDiagnostics,omitempty"`
}

func cloneContract(c *Contract) *Contract {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Permissions != nil {
		cp.Permissions = append([]string(nil), c.Permissions...)
	}
	if c.SupportedLocales != nil {
		cp.SupportedLocales = append([]string(nil), c.SupportedLocales...)
	}
	if c.Inputs != nil {
		cp.Inputs = make([]InputDefinition, len(c.Inputs))
		for i, in := range c.Inputs {
			inCopy := in
			if in.Examples != nil {
				inCopy.Examples = append([]string(nil), in.Examples...)
			}
			if in.Enum != nil {
				inCopy.Enum = append([]string(nil), in.Enum...)
			}
			if in.Min != nil {
				v := *in.Min
				inCopy.Min = &v
			}
			if in.Max != nil {
				v := *in.Max
				inCopy.Max = &v
			}
			if in.MinLength != nil {
				v := *in.MinLength
				inCopy.MinLength = &v
			}
			if in.MaxLength != nil {
				v := *in.MaxLength
				inCopy.MaxLength = &v
			}
			if in.Labels != nil {
				inCopy.Labels = make(map[string]string, len(in.Labels))
				for k, v := range in.Labels {
					inCopy.Labels[k] = v
				}
			}
			if in.Descriptions != nil {
				inCopy.Descriptions = make(map[string]string, len(in.Descriptions))
				for k, v := range in.Descriptions {
					inCopy.Descriptions[k] = v
				}
			}
			cp.Inputs[i] = inCopy
		}
	}
	return &cp
}

func cloneItem(v Item) Item {
	v.AppBundleIDs = append([]string(nil), v.AppBundleIDs...)
	if v.Provenance != nil {
		p := *v.Provenance
		v.Provenance = &p
	}
	v.Contract = cloneContract(v.Contract)
	if v.ContractDiagnostics != nil {
		v.ContractDiagnostics = append([]Diagnostic(nil), v.ContractDiagnostics...)
	}
	if v.SlotDefaults != nil {
		m := make(map[string]SlotDefault, len(v.SlotDefaults))
		for k, s := range v.SlotDefaults {
			if s.Default != nil {
				d := *s.Default
				s.Default = &d
			}
			m[k] = s
		}
		v.SlotDefaults = m
	}
	return v
}

// MergeView collapses any collision to one row and one diagnostic per ID.
// Local always wins, then imported, then bundled. Input slices are not modified.
func MergeView(local, imported, bundled []Item) ([]Item, []Diagnostic) {
	winners := map[string]Item{}
	counts := map[string]int{}
	for group, items := range [][]Item{local, imported, bundled} {
		for _, v := range items {
			counts[v.ID]++
			if _, exists := winners[v.ID]; exists {
				continue
			}
			v = cloneItem(v)
			switch group {
			case 0:
				v.Source = Local
				v.SourceKind = SourceLocal
				v.Provenance = nil
				v.UpdateStatus = "no_updates"
				v.ContractStatus = "degraded_no_contract"
				v.Contract = nil
				v.ContractDiagnostics = nil
				v.Orphaned = false
			case 1:
				v.Source = Imported
			case 2:
				v.Source = Bundled
				v.SourceKind = SourceBundled
			}
			winners[v.ID] = v
		}
	}
	keys := make([]string, 0, len(winners))
	for k := range winners {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Item
	var ds []Diagnostic
	for _, k := range keys {
		out = append(out, winners[k])
		if counts[k] > 1 {
			ds = append(ds, diagnostic(DuplicateID, k, "multiple sources use this ID; highest-priority source retained"))
		}
	}
	return out, ds
}

// Fork detaches provenance in memory. The caller may explicitly save it using
// its existing local prompt writer; this package never rewrites prompts files.
func Fork(item Item) Item {
	item = cloneItem(item)
	item.ID = "local:" + strings.TrimPrefix(item.ID, "local:")
	item.Source = Local
	item.SourceKind = SourceLocal
	item.Maturity = ""
	item.Provenance = nil
	item.Orphaned = false
	item.UpdateStatus = "no_updates"
	item.ContractStatus = "degraded_no_contract"
	item.Contract = nil
	item.ContractDiagnostics = nil
	return item
}

// DefaultSolutions returns canonical solutions, never discovery rows. Media is
// the DISTINCT union across the entire discovery index, not a search result.
// If discovery is omitted entirely, modality tags are the canonical fallback.
func DefaultSolutions(c Catalog) []Solution {
	media := map[string]map[string]bool{}
	for _, d := range c.Discovery {
		k := d.PackageID + "\x00" + d.SolutionID
		if media[k] == nil {
			media[k] = map[string]bool{}
		}
		for _, m := range d.Media {
			media[k][m] = true
		}
	}
	var out []Solution
	for _, s := range c.Solutions {
		m := media[s.PackageID+"\x00"+s.ID]
		if len(c.Discovery) == 0 {
			m = map[string]bool{}
			for _, tag := range s.Tags {
				if strings.HasPrefix(tag, "modality:") {
					m[strings.TrimPrefix(tag, "modality:")] = true
				}
			}
		}
		if len(m) == 1 && m["text"] {
			out = append(out, s)
		}
	}
	return out
}
