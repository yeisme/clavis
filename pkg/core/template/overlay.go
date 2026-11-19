package template

import (
	"bytes"
	"strings"

	"github.com/BurntSushi/toml"
)

// Overlay is the entire writable surface: five scalar/list fields plus
// slot.<name>.default. Pointers distinguish omitted from false, empty or [].
type Overlay struct {
	Trigger      *string                `toml:"trigger"`
	Title        *string                `toml:"title"`
	Category     *string                `toml:"category"`
	AppBundleIDs *[]string              `toml:"app_bundle_ids"`
	Slot         map[string]SlotDefault `toml:"slot"`
	Enabled      *bool                  `toml:"enabled"`
}

func ParseOverlay(b []byte) (Overlay, []Diagnostic) {
	var o Overlay
	bad := func() (Overlay, []Diagnostic) {
		return Overlay{}, []Diagnostic{diagnostic(InvalidOverlay, "overlay", "only trigger, title, category, app_bundle_ids, slot.<name>.default and enabled are writable")}
	}
	md, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&o)
	if err != nil || len(md.Undecoded()) != 0 {
		return bad()
	}
	for _, key := range md.Keys() {
		allowed := false
		if len(key) == 1 {
			switch key[0] {
			case "trigger", "title", "category", "app_bundle_ids", "enabled", "slot":
				allowed = true
			}
		}
		if len(key) == 2 && key[0] == "slot" {
			allowed = validSlotName(key[1])
		}
		if len(key) == 3 && key[0] == "slot" && validSlotName(key[1]) && key[2] == "default" {
			allowed = true
		}
		if !allowed {
			return bad()
		}
	}
	for name, s := range o.Slot {
		if !validSlotName(name) || s.Default == nil {
			return bad()
		}
	}
	return o, nil
}
func validSlotName(s string) bool { return safeToken(s) && !strings.ContainsAny(s, "\r\n\t") }

func ApplyOverlay(item Item, o Overlay) Item {
	item = cloneItem(item)
	if o.Trigger != nil {
		item.Trigger = *o.Trigger
	}
	if o.Title != nil {
		item.Title = *o.Title
	}
	if o.Category != nil {
		item.Category = *o.Category
	}
	if o.AppBundleIDs != nil {
		item.AppBundleIDs = append([]string(nil), (*o.AppBundleIDs)...)
	}
	if o.Enabled != nil {
		item.Enabled = *o.Enabled
	}
	if o.Slot != nil {
		if item.SlotDefaults == nil {
			item.SlotDefaults = map[string]SlotDefault{}
		}
		for name, s := range o.Slot {
			if s.Default != nil {
				d := *s.Default
				item.SlotDefaults[name] = SlotDefault{Default: &d}
			}
		}
	}
	return item
}

func (l *Library) SaveOverlay(solution, role, locale string, b []byte) []Diagnostic {
	l.mu.Lock()
	defer l.mu.Unlock()
	stem, err := identityStem(solution, role, locale)
	if err != nil {
		return []Diagnostic{err.(Diagnostic)}
	}
	if _, ds := ParseOverlay(b); len(ds) > 0 {
		return ds
	}
	// Never attach an overlay to an ambiguous or non-existent import identity.
	metadata, err := l.root.ReadFile("imports/" + stem + ".json")
	if err != nil {
		return []Diagnostic{diagnostic(DanglingReference, stem, "overlay requires an installed import")}
	}
	record, err := decodeRecord(metadata, stem)
	if err != nil {
		return []Diagnostic{err.(Diagnostic)}
	}
	if record.SolutionID != solution || record.Role != role || record.Locale != locale {
		return []Diagnostic{diagnostic(AmbiguousIdentity, stem, "overlay identity does not match the installed variant")}
	}
	if err = l.atomicWrite("overlays/"+stem+".toml", b); err != nil {
		return []Diagnostic{err.(Diagnostic)}
	}
	return nil
}
