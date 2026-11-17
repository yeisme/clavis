// Package inputmigration models the portable, privacy-preserving part of an
// input engine migration. It intentionally stops before encryption and
// transport; callers can pass the returned bytes to the sync envelope layer.
package inputmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	syncprotocol "github.com/yeisme/clavis/pkg/core/sync"
)

const (
	SchemaVersion            uint16 = 1
	MigrationProtocolVersion        = syncprotocol.ProtocolVersion
)

type Sensitivity string

const (
	SensitivityPublic  Sensitivity = "public"
	SensitivityPrivate Sensitivity = "private"
	SensitivitySecret  Sensitivity = "secret"
)

type FieldMetadata struct {
	Source      string      `json:"source"`
	Sensitivity Sensitivity `json:"sensitivity"`
}

type DictionaryMetadata struct {
	ID           string `json:"id"`
	ContentHash  string `json:"contentHash"`
	Format       string `json:"format,omitempty"`
	EntryCount   uint64 `json:"entryCount,omitempty"`
	SourceDigest string `json:"sourceDigest,omitempty"`
}

// LearningEntry contains digests, rather than words or input text. Use
// NewLearningEntry when importing a local learning store. Digesting here is a
// deliberate boundary: a migration payload can carry useful frequency data
// without becoming a transcript of what somebody typed.
type LearningEntry struct {
	KeyDigest  string `json:"keyDigest"`
	WordDigest string `json:"wordDigest"`
	Frequency  uint64 `json:"frequency"`
	Pinned     bool   `json:"pinned,omitempty"`
}

type BigramEntry struct {
	PreviousDigest string `json:"previousDigest"`
	WordDigest     string `json:"wordDigest"`
	Frequency      uint64 `json:"frequency"`
}

type LearningData struct {
	Enabled bool            `json:"enabled"`
	Words   []LearningEntry `json:"words,omitempty"`
	Bigrams []BigramEntry   `json:"bigrams,omitempty"`
	// Aggregates are stable digest/frequency pairs used by implementations
	// which merge data from more than one local store.
	Aggregates []LearningEntry `json:"aggregates,omitempty"`
}

type StableApp struct {
	BundleID string `json:"bundleId,omitempty"`
	Process  string `json:"process,omitempty"`
}

type ShortcutRule struct {
	Chord        string      `json:"chord"`
	Action       string      `json:"action"`
	Apps         []StableApp `json:"apps,omitempty"`
	Explicit     bool        `json:"explicit,omitempty"`
	RequiresHook bool        `json:"requiresHook,omitempty"`
}

type AppRule struct {
	ID     string      `json:"id"`
	Apps   []StableApp `json:"apps"`
	Action string      `json:"action"`
	// Context is intentionally not represented. In particular, an app rule
	// cannot carry a title, URL, document name, or password-field state.
}

// PersonalBaseline is the account-scoped input-engine layer. It contains
// portable preferences and opaque learning metadata, but no device paths,
// window context, API keys, or plaintext learned words.
type PersonalBaseline struct {
	SchemaVersion    uint16                   `json:"schemaVersion"`
	ProtocolVersion  uint16                   `json:"protocolVersion"`
	AccountID        string                   `json:"accountId"`
	Scope            string                   `json:"scope"`
	Revision         uint64                   `json:"revision,omitempty"`
	FieldRevisions   map[string]uint64        `json:"fieldRevisions,omitempty"`
	Scheme           string                   `json:"scheme"`
	CandidateCount   int                      `json:"candidateCount"`
	ShiftToggleASCII bool                     `json:"shiftToggleASCII"`
	FuzzyPinyin      []string                 `json:"fuzzyPinyin,omitempty"`
	Dictionaries     []DictionaryMetadata     `json:"dictionaries,omitempty"`
	Learning         LearningData             `json:"learning"`
	Shortcuts        []ShortcutRule           `json:"shortcuts,omitempty"`
	AppRules         []AppRule                `json:"appRules,omitempty"`
	FieldMetadata    map[string]FieldMetadata `json:"fieldMetadata"`
	SourceDevice     *DeviceCapabilities      `json:"sourceDevice,omitempty"`
}

// PermissionState is deliberately an enum: arbitrary permission details must
// not be smuggled into an export.
type PermissionState string

const (
	PermissionGranted PermissionState = "granted"
	PermissionDenied  PermissionState = "denied"
	PermissionUnknown PermissionState = "unknown"
)

type DisplayInfo struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
}

type DeviceCapabilities struct {
	OS             string                     `json:"os"`
	Arch           string                     `json:"arch"`
	KeyboardLayout string                     `json:"keyboardLayout"`
	InputSourceIDs []string                   `json:"inputSourceIds"`
	NativeIMK      bool                       `json:"nativeImk"`
	NativeTSF      bool                       `json:"nativeTsf"`
	HookCapability bool                       `json:"hookCapability"`
	Permissions    map[string]PermissionState `json:"permissions"`
	Display        DisplayInfo                `json:"display"`
}

type DeferredRule struct {
	RuleID string `json:"ruleId"`
	Reason string `json:"reason"`
	State  string `json:"state"` // deferred or unsupported
}

type DeviceOverlay struct {
	SchemaVersion   uint16                     `json:"schemaVersion"`
	ProtocolVersion uint16                     `json:"protocolVersion"`
	AccountID       string                     `json:"accountId"`
	DeviceID        string                     `json:"deviceId"`
	Scope           string                     `json:"scope"`
	KeyboardLayout  string                     `json:"keyboardLayout"`
	InputSourceID   string                     `json:"inputSourceId"`
	InputSourceIDs  []string                   `json:"inputSourceIds"`
	Backend         string                     `json:"backend"`
	HookEnabled     bool                       `json:"hookEnabled"`
	Permissions     map[string]PermissionState `json:"permissions"`
	Display         DisplayInfo                `json:"display"`
	Shortcuts       []ShortcutRule             `json:"shortcuts,omitempty"`
	AppRules        []AppRule                  `json:"appRules,omitempty"`
	DeferredRules   []DeferredRule             `json:"deferredRules,omitempty"`
	FieldMetadata   map[string]FieldMetadata   `json:"fieldMetadata"`
}

type Export struct {
	SchemaVersion   uint16           `json:"schemaVersion"`
	ProtocolVersion uint16           `json:"protocolVersion"`
	AccountID       string           `json:"accountId"`
	Scope           string           `json:"scope"`
	Baseline        PersonalBaseline `json:"baseline"`
	Overlay         *DeviceOverlay   `json:"overlay,omitempty"`
}

type DiffCategory string

const (
	CategoryPlatform    DiffCategory = "platform"
	CategoryKeyboard    DiffCategory = "keyboard"
	CategoryInputSource DiffCategory = "input-source"
	CategoryPermission  DiffCategory = "permission"
	CategoryDisplay     DiffCategory = "display"
	CategoryAppRule     DiffCategory = "app-rule"
)

type Diff struct {
	Path        string       `json:"path"`
	Category    DiffCategory `json:"category"`
	SourceValue string       `json:"sourceValue"`
	TargetValue string       `json:"targetValue"`
	Reason      string       `json:"reason"`
}

type Preview struct {
	Overlay DeviceOverlay `json:"overlay"`
	Diffs   []Diff        `json:"diffs"`
}

type FieldConflict struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Target string `json:"target"`
	Winner string `json:"winner"`
}

func NewLearningEntry(pinyinKey, word string, frequency uint64, pinned bool) LearningEntry {
	return LearningEntry{KeyDigest: digest(pinyinKey), WordDigest: digest(word), Frequency: frequency, Pinned: pinned}
}
func NewBigramEntry(previous, word string, frequency uint64) BigramEntry {
	return BigramEntry{PreviousDigest: digest(previous), WordDigest: digest(word), Frequency: frequency}
}
func Digest(value string) string { return digest(value) }
func digest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func NewPersonalBaseline(account string) PersonalBaseline {
	return PersonalBaseline{SchemaVersion: SchemaVersion, ProtocolVersion: MigrationProtocolVersion, AccountID: account, Scope: "personal", FieldMetadata: defaultBaselineMetadata(), FieldRevisions: map[string]uint64{}}
}
func NewDeviceOverlay(account, device string) DeviceOverlay {
	return DeviceOverlay{SchemaVersion: SchemaVersion, ProtocolVersion: MigrationProtocolVersion, AccountID: account, DeviceID: device, Scope: "device", Permissions: map[string]PermissionState{}, FieldMetadata: defaultOverlayMetadata()}
}

func defaultBaselineMetadata() map[string]FieldMetadata {
	return map[string]FieldMetadata{
		"scheme": {"user", SensitivityPublic}, "candidateCount": {"user", SensitivityPublic}, "shiftToggleASCII": {"user", SensitivityPublic},
		"fuzzyPinyin": {"user", SensitivityPrivate}, "dictionaries": {"local-metadata", SensitivityPrivate}, "learning": {"learning-store", SensitivityPrivate},
		"shortcuts": {"user", SensitivityPublic}, "appRules": {"user", SensitivityPublic},
	}
}
func defaultOverlayMetadata() map[string]FieldMetadata {
	return map[string]FieldMetadata{
		"keyboardLayout": {"device-capability", SensitivityPublic}, "inputSourceId": {"device-capability", SensitivityPublic},
		"permissions": {"device-capability", SensitivityPrivate}, "display": {"device-capability", SensitivityPublic},
		"appRules": {"user", SensitivityPublic},
	}
}

func validOS(os string) bool {
	switch strings.ToLower(strings.TrimSpace(os)) {
	case "windows", "darwin", "macos", "linux":
		return true
	}
	return false
}
func validateApp(a StableApp) error {
	if a.BundleID == "" && a.Process == "" {
		return errors.New("inputmigration: app reference is empty")
	}
	if strings.ContainsAny(a.BundleID+a.Process, "\r\n") {
		return errors.New("inputmigration: invalid app reference")
	}
	return nil
}
func (c DeviceCapabilities) Validate() error {
	if !validOS(c.OS) {
		return fmt.Errorf("inputmigration: invalid platform %q", c.OS)
	}
	if strings.TrimSpace(c.Arch) == "" || strings.TrimSpace(c.KeyboardLayout) == "" {
		return errors.New("inputmigration: architecture and keyboard layout are required")
	}
	if len(c.InputSourceIDs) == 0 {
		return errors.New("inputmigration: at least one input source is required")
	}
	for _, id := range c.InputSourceIDs {
		if strings.TrimSpace(id) == "" {
			return errors.New("inputmigration: empty input source")
		}
	}
	for key, p := range c.Permissions {
		if key == "" || (p != PermissionGranted && p != PermissionDenied && p != PermissionUnknown) {
			return errors.New("inputmigration: invalid permission")
		}
	}
	if c.Display.Width < 0 || c.Display.Height < 0 || c.Display.Scale < 0 {
		return errors.New("inputmigration: invalid display")
	}
	return nil
}
func (b PersonalBaseline) Validate() error {
	if b.SchemaVersion != SchemaVersion || b.ProtocolVersion != MigrationProtocolVersion {
		return errors.New("inputmigration: unsupported baseline version")
	}
	if strings.TrimSpace(b.AccountID) == "" || b.Scope != "personal" {
		return errors.New("inputmigration: invalid baseline scope")
	}
	if strings.TrimSpace(b.Scheme) == "" || b.CandidateCount < 1 || b.CandidateCount > 99 {
		return errors.New("inputmigration: invalid input-engine settings")
	}
	if b.FieldMetadata == nil {
		return errors.New("inputmigration: missing field metadata")
	}
	for _, d := range b.Dictionaries {
		if d.ID == "" || d.ContentHash == "" {
			return errors.New("inputmigration: invalid dictionary metadata")
		}
	}
	for _, w := range b.Learning.Words {
		if !validDigest(w.KeyDigest) || !validDigest(w.WordDigest) {
			return errors.New("inputmigration: learning contains non-digest data")
		}
	}
	for _, w := range b.Learning.Bigrams {
		if !validDigest(w.PreviousDigest) || !validDigest(w.WordDigest) {
			return errors.New("inputmigration: bigram contains non-digest data")
		}
	}
	for _, w := range b.Learning.Aggregates {
		if !validDigest(w.KeyDigest) || !validDigest(w.WordDigest) {
			return errors.New("inputmigration: aggregate contains non-digest data")
		}
	}
	for _, r := range b.Shortcuts {
		for _, a := range r.Apps {
			if err := validateApp(a); err != nil {
				return err
			}
		}
	}
	for _, r := range b.AppRules {
		if r.ID == "" || r.Action == "" {
			return errors.New("inputmigration: invalid app rule")
		}
		for _, a := range r.Apps {
			if err := validateApp(a); err != nil {
				return err
			}
		}
	}
	if b.SourceDevice != nil {
		return b.SourceDevice.Validate()
	}
	return nil
}
func validDigest(v string) bool {
	if len(v) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}
func (o DeviceOverlay) Validate() error {
	if o.SchemaVersion != SchemaVersion || o.ProtocolVersion != MigrationProtocolVersion {
		return errors.New("inputmigration: unsupported overlay version")
	}
	if strings.TrimSpace(o.AccountID) == "" || strings.TrimSpace(o.DeviceID) == "" || o.Scope != "device" {
		return errors.New("inputmigration: invalid overlay scope")
	}
	if strings.TrimSpace(o.KeyboardLayout) == "" || strings.TrimSpace(o.InputSourceID) == "" || strings.TrimSpace(o.Backend) == "" {
		return errors.New("inputmigration: incomplete overlay")
	}
	for _, r := range o.AppRules {
		if r.ID == "" {
			return errors.New("inputmigration: invalid overlay app rule")
		}
	}
	return nil
}

func marshalStrict(v any, validate func() error) ([]byte, error) {
	if err := validate(); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func unmarshalStrict(data []byte, v any, validate func() error) error {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("inputmigration: invalid JSON: %w", err)
	}
	if err := validate(); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("inputmigration: trailing JSON")
	}
	return nil
}
func MarshalBaseline(b PersonalBaseline) ([]byte, error) { return marshalStrict(b, b.Validate) }
func UnmarshalBaseline(data []byte) (PersonalBaseline, error) {
	var b PersonalBaseline
	return b, unmarshalStrict(data, &b, func() error { return b.Validate() })
}
func MarshalOverlay(o DeviceOverlay) ([]byte, error) { return marshalStrict(o, o.Validate) }
func UnmarshalOverlay(data []byte) (DeviceOverlay, error) {
	var o DeviceOverlay
	return o, unmarshalStrict(data, &o, func() error { return o.Validate() })
}
func MarshalExport(e Export) ([]byte, error) { return marshalStrict(e, e.Validate) }
func UnmarshalExport(data []byte) (Export, error) {
	var e Export
	return e, unmarshalStrict(data, &e, func() error { return e.Validate() })
}
func (e Export) Validate() error {
	if e.SchemaVersion != SchemaVersion || e.ProtocolVersion != MigrationProtocolVersion || e.Scope != "layered" || e.AccountID == "" {
		return errors.New("inputmigration: invalid export version or scope")
	}
	if err := e.Baseline.Validate(); err != nil {
		return err
	}
	if e.Baseline.AccountID != e.AccountID {
		return errors.New("inputmigration: export account mismatch")
	}
	if e.Overlay != nil {
		if err := e.Overlay.Validate(); err != nil {
			return err
		}
		if e.Overlay.AccountID != e.AccountID {
			return errors.New("inputmigration: overlay account mismatch")
		}
	}
	return nil
}
func NewExport(b PersonalBaseline, o *DeviceOverlay) (Export, error) {
	if err := b.Validate(); err != nil {
		return Export{}, err
	}
	e := Export{SchemaVersion: SchemaVersion, ProtocolVersion: MigrationProtocolVersion, AccountID: b.AccountID, Scope: "layered", Baseline: cloneBaseline(b)}
	if o != nil {
		x := cloneOverlay(*o)
		e.Overlay = &x
	}
	return e, e.Validate()
}

func cloneBaseline(b PersonalBaseline) PersonalBaseline {
	raw, _ := json.Marshal(b)
	var out PersonalBaseline
	_ = json.Unmarshal(raw, &out)
	return out
}
func cloneOverlay(o DeviceOverlay) DeviceOverlay {
	raw, _ := json.Marshal(o)
	var out DeviceOverlay
	_ = json.Unmarshal(raw, &out)
	return out
}

// GenerateOverlay derives only device-local state from capabilities. User
// rules are retained, while rules requiring unavailable hooks are explicit in
// DeferredRules rather than silently dropped.
func GenerateOverlay(b PersonalBaseline, target DeviceCapabilities, deviceID string) (Preview, error) {
	if err := b.Validate(); err != nil {
		return Preview{}, err
	}
	if err := target.Validate(); err != nil {
		return Preview{}, err
	}
	if strings.TrimSpace(deviceID) == "" {
		return Preview{}, errors.New("inputmigration: device id is required")
	}
	o := NewDeviceOverlay(b.AccountID, deviceID)
	o.KeyboardLayout, o.InputSourceIDs, o.InputSourceID = target.KeyboardLayout, append([]string(nil), target.InputSourceIDs...), target.InputSourceIDs[0]
	o.Permissions, o.Display = copyPermissions(target.Permissions), target.Display
	o.Backend = backendFor(target)
	o.HookEnabled = target.HookCapability && o.Backend == "hook"
	o.Shortcuts, o.AppRules = append([]ShortcutRule(nil), b.Shortcuts...), append([]AppRule(nil), b.AppRules...)
	for _, r := range o.Shortcuts {
		if r.RequiresHook && !target.HookCapability {
			o.DeferredRules = append(o.DeferredRules, DeferredRule{RuleID: r.Chord, State: "unsupported", Reason: "target lacks hook capability"})
		}
	}
	if err := o.Validate(); err != nil {
		return Preview{}, err
	}
	p := Preview{Overlay: cloneOverlay(o)}
	add := func(path string, cat DiffCategory, source, targetValue, reason string) {
		p.Diffs = append(p.Diffs, Diff{path, cat, safeSummary(source), safeSummary(targetValue), reason})
	}
	if b.SourceDevice == nil {
		add("/platform", CategoryPlatform, "unspecified", target.OS, "platform")
	} else {
		if !strings.EqualFold(b.SourceDevice.OS, target.OS) {
			add("/platform/os", CategoryPlatform, b.SourceDevice.OS, target.OS, "platform")
		}
		if b.SourceDevice.Arch != target.Arch {
			add("/platform/arch", CategoryPlatform, b.SourceDevice.Arch, target.Arch, "platform")
		}
		if b.SourceDevice.KeyboardLayout != target.KeyboardLayout {
			add("/keyboard/layout", CategoryKeyboard, b.SourceDevice.KeyboardLayout, target.KeyboardLayout, "keyboard")
		}
		if !sameStringSet(b.SourceDevice.InputSourceIDs, target.InputSourceIDs) {
			add("/input-source/ids", CategoryInputSource, strings.Join(b.SourceDevice.InputSourceIDs, ","), strings.Join(target.InputSourceIDs, ","), "input-source")
		}
		if backendFor(*b.SourceDevice) != o.Backend {
			add("/platform/backend", CategoryPlatform, backendFor(*b.SourceDevice), o.Backend, "platform")
		}
	}
	if !samePermissions(b.SourceDevice, target) {
		add("/permissions", CategoryPermission, permissionSummary(b.SourceDevice), permissionSummary(&target), "permission")
	}
	if b.SourceDevice != nil && b.SourceDevice.Display != target.Display {
		add("/display", CategoryDisplay, fmt.Sprintf("%dx%d", b.SourceDevice.Display.Width, b.SourceDevice.Display.Height), fmt.Sprintf("%dx%d", target.Display.Width, target.Display.Height), "display")
	}
	for _, r := range o.DeferredRules {
		add("/shortcuts/"+r.RuleID, CategoryAppRule, "supported", r.State, "app-rule")
	}
	return p, nil
}
func backendFor(c DeviceCapabilities) string {
	if strings.EqualFold(c.OS, "windows") && c.NativeTSF {
		return "native-tsf"
	}
	if (strings.EqualFold(c.OS, "darwin") || strings.EqualFold(c.OS, "macos")) && c.NativeIMK {
		return "native-imk"
	}
	if c.HookCapability {
		return "hook"
	}
	return "deferred"
}
func safeSummary(v string) string {
	if v == "" {
		return "<empty>"
	}
	if len(v) > 64 {
		v = v[:64]
	}
	return digest(v)[:12]
}
func permissionSummary(c *DeviceCapabilities) string {
	if c == nil {
		return "<none>"
	}
	keys := make([]string, 0, len(c.Permissions))
	for k := range c.Permissions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(string(c.Permissions[k]))
		b.WriteByte(';')
	}
	return b.String()
}
func samePermissions(a *DeviceCapabilities, b DeviceCapabilities) bool {
	return permissionSummary(a) == permissionSummary(&b)
}
func copyPermissions(in map[string]PermissionState) map[string]PermissionState {
	out := make(map[string]PermissionState, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func sameStringSet(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// MergeBaselines applies field-level revisions. Equal-revision conflicts are
// deterministic (the target wins), and device fields are never involved in
// this operation. Local overlay settings therefore cannot be overwritten by a
// baseline import.
func MergeBaselines(source, target PersonalBaseline) (PersonalBaseline, []FieldConflict, error) {
	if err := source.Validate(); err != nil {
		return PersonalBaseline{}, nil, err
	}
	if err := target.Validate(); err != nil {
		return PersonalBaseline{}, nil, err
	}
	if source.AccountID != target.AccountID {
		return PersonalBaseline{}, nil, errors.New("inputmigration: account mismatch")
	}
	out := cloneBaseline(source)
	conflicts := []FieldConflict{}
	choose := func(path string, different bool, apply func()) {
		if !different {
			return
		}
		sr, tr := source.FieldRevisions[path], target.FieldRevisions[path]
		if tr >= sr {
			apply()
		}
		if tr == sr {
			conflicts = append(conflicts, FieldConflict{Path: path, Source: "source", Target: "target", Winner: "target"})
		}
	}
	choose("scheme", source.Scheme != target.Scheme, func() { out.Scheme = target.Scheme })
	choose("candidateCount", source.CandidateCount != target.CandidateCount, func() { out.CandidateCount = target.CandidateCount })
	choose("shiftToggleASCII", source.ShiftToggleASCII != target.ShiftToggleASCII, func() { out.ShiftToggleASCII = target.ShiftToggleASCII })
	choose("fuzzyPinyin", !sameStringSet(source.FuzzyPinyin, target.FuzzyPinyin), func() { out.FuzzyPinyin = append([]string(nil), target.FuzzyPinyin...) })
	choose("learning", !sameLearning(source.Learning, target.Learning), func() { out.Learning = cloneLearning(target.Learning) })
	choose("shortcuts", !sameJSON(source.Shortcuts, target.Shortcuts), func() { out.Shortcuts = append([]ShortcutRule(nil), target.Shortcuts...) })
	choose("appRules", !sameJSON(source.AppRules, target.AppRules), func() { out.AppRules = append([]AppRule(nil), target.AppRules...) })
	return out, conflicts, out.Validate()
}
func cloneLearning(v LearningData) LearningData {
	v.Words = append([]LearningEntry(nil), v.Words...)
	v.Bigrams = append([]BigramEntry(nil), v.Bigrams...)
	v.Aggregates = append([]LearningEntry(nil), v.Aggregates...)
	return v
}
func sameLearning(a, b LearningData) bool { return sameJSON(a, b) }
func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
