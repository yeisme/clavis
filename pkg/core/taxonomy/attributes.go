package taxonomy

// AttributeKind classifies a structured entity attribute so UI renderers can
// automatically select typography (mono, tabular numerals) and visual style.
type AttributeKind string

const (
	AttrText     AttributeKind = "text"
	AttrMono     AttributeKind = "mono"
	AttrStatus   AttributeKind = "status"
	AttrCount    AttributeKind = "count"
	AttrTime     AttributeKind = "time"
	AttrScope    AttributeKind = "scope"
	AttrSecurity AttributeKind = "security"
)

const (
	ToneNeutral = "neutral"
	ToneAccent  = "accent"
	ToneSuccess = "success"
	ToneWarning = "warning"
	ToneDanger  = "danger"
)

// Attribute represents a typed metadata property rendered in property grids.
type Attribute struct {
	Key      string        `json:"key"`
	Label    string        `json:"label"`
	Value    string        `json:"value"`
	Icon     string        `json:"icon,omitempty"`
	Kind     AttributeKind `json:"kind"`
	Tone     string        `json:"tone,omitempty"`
	Copyable bool          `json:"copyable,omitempty"`
}

// IsValid reports whether k is one of the 7 supported AttributeKind values.
func (k AttributeKind) IsValid() bool {
	switch k {
	case AttrText, AttrMono, AttrStatus, AttrCount, AttrTime, AttrScope, AttrSecurity:
		return true
	default:
		return false
	}
}

// UsesMonoFont reports whether values of kind k should render with Technical Mono.
func (k AttributeKind) UsesMonoFont() bool {
	return k == AttrMono
}

// UsesTabularNums reports whether values of kind k should enable tabular-nums (tnum).
func (k AttributeKind) UsesTabularNums() bool {
	return k == AttrCount || k == AttrTime
}
