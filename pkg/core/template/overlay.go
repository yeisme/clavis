package template

import (
	"strings"

	coretoml "github.com/yeisme/clavis/pkg/core/toml"
)

// Overlay is the entire writable surface: five scalar/list fields plus
// slot.<name>.default. Pointers distinguish omitted from false, empty or [].
type Overlay struct {
	Trigger      *string
	Title        *string
	Category     *string
	AppBundleIDs *[]string
	Slot         map[string]SlotDefault
	Enabled      *bool
}

func ParseOverlay(b []byte) (Overlay, []Diagnostic) {
	bad := func() (Overlay, []Diagnostic) {
		return Overlay{}, []Diagnostic{diagnostic(InvalidOverlay, "overlay", "only trigger, title, category, app_bundle_ids, slot.<name>.default and enabled are writable")}
	}
	tbl, err := coretoml.Parse(string(b))
	if err != nil {
		return bad()
	}
	var o Overlay
	for _, key := range tbl.SortedKeys() {
		value, _ := tbl.Get(key)
		switch key {
		case "trigger":
			v, ok := value.AsString()
			if !ok {
				return bad()
			}
			o.Trigger = &v
		case "title":
			v, ok := value.AsString()
			if !ok {
				return bad()
			}
			o.Title = &v
		case "category":
			v, ok := value.AsString()
			if !ok {
				return bad()
			}
			o.Category = &v
		case "enabled":
			v, ok := value.AsBool()
			if !ok {
				return bad()
			}
			o.Enabled = &v
		case "app_bundle_ids":
			values, ok := value.AsArray()
			if !ok {
				return bad()
			}
			ids := make([]string, len(values))
			for i, item := range values {
				ids[i], ok = item.AsString()
				if !ok {
					return bad()
				}
			}
			o.AppBundleIDs = &ids
		case "slot":
			slots, ok := value.AsTable()
			if !ok {
				return bad()
			}
			o.Slot = make(map[string]SlotDefault, len(slots.Entries))
			for _, name := range slots.SortedKeys() {
				if !validSlotName(name) {
					return bad()
				}
				entry, _ := slots.Get(name)
				slot, ok := entry.AsTable()
				if !ok || len(slot.Entries) != 1 {
					return bad()
				}
				def, ok := slot.Get("default")
				if !ok {
					return bad()
				}
				v, ok := def.AsString()
				if !ok {
					return bad()
				}
				o.Slot[name] = SlotDefault{Default: &v}
			}
		default:
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
