// Package inputmigration models the portable, privacy-preserving part of an
// input engine migration. It intentionally stops before encryption and
// transport; callers can pass the returned bytes to the sync envelope layer.
package inputmigration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	syncprotocol "github.com/yeisme/clavis/pkg/core/sync"
)

const (
	SchemaVersion            uint16 = 1
	MigrationProtocolVersion        = syncprotocol.ProtocolVersion

	// Bounds are deliberately conservative: migration payloads are settings, not
	// bulk stores.  The encrypted learning-store object is transported by the
	// sync layer separately.
	maxJSONSize       = 1 << 20
	maxCollectionSize = 4096
	maxMetadataSize   = 256
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
	Revision     uint64 `json:"revision,omitempty"`
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

// LearningData is a privacy-preserving summary only. It is useful for
// conflict resolution and ranking hints, but it cannot reconstruct a
// LearningStore: digests are one-way and no plaintext words are present.
// The actual store, when recovery is requested, must be supplied as a
// separately encrypted object by the sync/envelope layer.
type LearningData struct {
	Enabled bool            `json:"enabled"`
	Words   []LearningEntry `json:"words,omitempty"`
	Bigrams []BigramEntry   `json:"bigrams,omitempty"`
	// Aggregates are stable digest/frequency pairs used for merge/ranking hints.
	Aggregates        []LearningEntry `json:"aggregates,omitempty"`
	SummaryOnly       bool            `json:"summaryOnly"`
	RecoveryObjectRef string          `json:"recoveryObjectRef,omitempty"`
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
	ID             string      `json:"id"`
	Apps           []StableApp `json:"apps"`
	Action         string      `json:"action,omitempty"` // legacy serialized action
	Mode           string      `json:"mode"`             // ascii, native, keep
	Scheme         string      `json:"scheme,omitempty"`
	InputSourceID  string      `json:"inputSourceId,omitempty"`
	RestoreOnLeave bool        `json:"restoreOnLeave"`
	// legacyV1Action records an unknown, non-empty action accepted only while
	// importing a v1 payload. It is deliberately not serialized: the original
	// Action value remains available for display and round-trip preservation.
	legacyV1Action string
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
	// legacyV1Scheme is set only by the v1 importer when Scheme is a non-empty
	// value outside the current engine whitelist. Keeping it separate from the
	// wire shape preserves old values without weakening validation for newly
	// constructed baselines.
	legacyV1Scheme string
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

// ErrDeferredOverlay reports that an otherwise well-formed overlay cannot be
// executed on a target because its backend is deferred. The overlay is still
// parseable so callers can inspect and present its deferred state.
var ErrDeferredOverlay = errors.New("inputmigration: deferred overlay is not executable")

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

// canonicalOS accepts the platform spellings used by callers and returns the
// spelling used by migration decisions. Surrounding whitespace is tolerated,
// and macOS is represented by the Go/runtime spelling darwin. An empty result
// means the platform is unknown or empty.
func canonicalOS(os string) string {
	switch strings.ToLower(strings.TrimSpace(os)) {
	case "windows":
		return "windows"
	case "darwin", "macos":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return ""
	}
}

func validScheme(s string) bool {
	switch s {
	case "quanpin", "xiaohe", "ziranma", "microsoft", "sogou", "pinyin_plus":
		return true
	}
	return false
}
func normalizedRuleMode(r AppRule) (string, error) {
	if r.Scheme != "" && !validScheme(r.Scheme) {
		return "", fmt.Errorf("inputmigration: unknown app rule scheme %q", r.Scheme)
	}
	mode := strings.TrimSpace(r.Mode)
	if r.Action != "" {
		// Compatibility conversion for the old portable action representation.
		actionMode := ""
		if strings.HasPrefix(r.Action, "scheme:") {
			actionScheme := strings.TrimPrefix(r.Action, "scheme:")
			if !validScheme(actionScheme) {
				return "", fmt.Errorf("inputmigration: unknown app rule scheme %q", r.Action)
			}
			if r.Scheme != "" && r.Scheme != actionScheme {
				return "", errors.New("inputmigration: app rule action and scheme conflict")
			}
			actionMode = "native"
		} else {
			switch r.Action {
			case "ascii", "native", "keep":
				actionMode = r.Action
			default:
				return "", fmt.Errorf("inputmigration: unknown app rule action %q", r.Action)
			}
		}
		if mode != "" && mode != actionMode {
			return "", errors.New("inputmigration: app rule action and mode conflict")
		}
		mode = actionMode
	}
	switch mode {
	case "ascii", "native", "keep":
		return mode, nil
	default:
		return "", fmt.Errorf("inputmigration: unknown app rule mode %q", mode)
	}
}
func validateApp(a StableApp) error {
	if a.BundleID == "" && a.Process == "" {
		return errors.New("inputmigration: app reference is empty")
	}
	if len(a.BundleID) > maxMetadataSize || len(a.Process) > maxMetadataSize || strings.ContainsAny(a.BundleID+a.Process, "\r\n") {
		return errors.New("inputmigration: invalid app reference")
	}
	return nil
}
func validateLegacyV1RuleActions(rules []AppRule) error {
	for _, r := range rules {
		if r.legacyV1Action != "" && r.Action == r.legacyV1Action {
			return fmt.Errorf("%w: app rule %q has unsupported legacy v1 action %q", ErrDeferredOverlay, r.ID, r.Action)
		}
	}
	return nil
}

func validateShortcut(r ShortcutRule) error {
	if strings.TrimSpace(r.Chord) == "" || strings.TrimSpace(r.Action) == "" || len(r.Chord) > maxMetadataSize || len(r.Action) > maxMetadataSize {
		return errors.New("inputmigration: invalid shortcut rule")
	}
	for _, a := range r.Apps {
		if err := validateApp(a); err != nil {
			return err
		}
	}
	return nil
}

func validateAppRule(r AppRule) error {
	if strings.TrimSpace(r.ID) == "" || len(r.ID) > maxMetadataSize || len(r.InputSourceID) > maxMetadataSize || len(r.Action) > maxMetadataSize || len(r.Mode) > maxMetadataSize || len(r.Scheme) > maxMetadataSize || len(r.Apps) > maxCollectionSize {
		return errors.New("inputmigration: invalid app rule")
	}
	if r.legacyV1Action != "" {
		if r.Action != r.legacyV1Action || strings.TrimSpace(r.Action) == "" {
			return errors.New("inputmigration: invalid legacy app rule action")
		}
		if r.Scheme != "" && !validScheme(r.Scheme) {
			return fmt.Errorf("inputmigration: unknown app rule scheme %q", r.Scheme)
		}
		if r.Mode != "" {
			switch r.Mode {
			case "ascii", "native", "keep":
			default:
				return fmt.Errorf("inputmigration: unknown app rule mode %q", r.Mode)
			}
		}
	} else if _, err := normalizedRuleMode(r); err != nil {
		return err
	}
	for _, a := range r.Apps {
		if err := validateApp(a); err != nil {
			return err
		}
	}
	return nil
}
func (c DeviceCapabilities) Validate() error {
	// Policy: leading/trailing whitespace is accepted for compatibility, but
	// callers must use canonicalOS for all platform-dependent decisions. This
	// value receiver deliberately does not mutate the caller's capabilities.
	if canonicalOS(c.OS) == "" {
		return fmt.Errorf("inputmigration: invalid platform %q", c.OS)
	}
	if strings.TrimSpace(c.Arch) == "" || strings.TrimSpace(c.KeyboardLayout) == "" {
		return errors.New("inputmigration: architecture and keyboard layout are required")
	}
	if len(c.InputSourceIDs) == 0 || len(c.InputSourceIDs) > maxCollectionSize {
		return errors.New("inputmigration: at least one input source is required")
	}
	for _, id := range c.InputSourceIDs {
		if strings.TrimSpace(id) == "" || len(id) > maxMetadataSize {
			return errors.New("inputmigration: empty input source")
		}
	}
	if len(c.Permissions) > maxMetadataSize {
		return errors.New("inputmigration: too many permissions")
	}
	for key, p := range c.Permissions {
		if key == "" || len(key) > maxMetadataSize || (p != PermissionGranted && p != PermissionDenied && p != PermissionUnknown) {
			return errors.New("inputmigration: invalid permission")
		}
	}
	if err := validateDisplay(c.Display); err != nil {
		return err
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
	if (!validScheme(b.Scheme) && (b.legacyV1Scheme == "" || b.legacyV1Scheme != b.Scheme)) || b.CandidateCount < 1 || b.CandidateCount > 99 {
		return errors.New("inputmigration: invalid input-engine settings")
	}
	if len(b.FuzzyPinyin) > maxCollectionSize || len(b.Dictionaries) > maxCollectionSize || len(b.Shortcuts) > maxCollectionSize || len(b.AppRules) > maxCollectionSize {
		return errors.New("inputmigration: collection too large")
	}
	if err := validateMetadata(b.FieldMetadata); err != nil {
		return err
	}
	if len(b.Learning.Words) > maxCollectionSize || len(b.Learning.Bigrams) > maxCollectionSize || len(b.Learning.Aggregates) > maxCollectionSize {
		return errors.New("inputmigration: learning summary too large")
	}
	if b.Learning.SummaryOnly == false && (len(b.Learning.Words) > 0 || len(b.Learning.Bigrams) > 0 || len(b.Learning.Aggregates) > 0) {
		return errors.New("inputmigration: learning summaries must declare summaryOnly")
	}
	if b.Learning.RecoveryObjectRef != "" && len(b.Learning.RecoveryObjectRef) > maxMetadataSize {
		return errors.New("inputmigration: recovery object reference too long")
	}
	for _, d := range b.Dictionaries {
		if strings.TrimSpace(d.ID) == "" || !validDigest(d.ContentHash) {
			return errors.New("inputmigration: invalid dictionary metadata")
		}
		if len(d.ID) > maxMetadataSize || len(d.Format) > maxMetadataSize || len(d.SourceDigest) > maxMetadataSize {
			return errors.New("inputmigration: dictionary metadata too long")
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
		if err := validateShortcut(r); err != nil {
			return err
		}
	}
	for _, r := range b.AppRules {
		if err := validateAppRule(r); err != nil {
			return err
		}
	}
	if b.SourceDevice != nil {
		return b.SourceDevice.Validate()
	}
	return nil
}
func validDigest(v string) bool {
	if len(v) != sha256.Size*2 || v != strings.ToLower(v) {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}
func validateDisplay(d DisplayInfo) error {
	if d.Width < 0 || d.Height < 0 || math.IsNaN(d.Scale) || math.IsInf(d.Scale, 0) || d.Scale <= 0 {
		return errors.New("inputmigration: invalid display")
	}
	return nil
}

func validateMetadata(m map[string]FieldMetadata) error {
	if m == nil || len(m) > maxMetadataSize {
		return errors.New("inputmigration: missing field metadata")
	}
	for key, value := range m {
		if strings.TrimSpace(key) == "" || len(key) > maxMetadataSize || len(value.Source) > maxMetadataSize {
			return errors.New("inputmigration: invalid field metadata")
		}
		if value.Sensitivity != SensitivityPublic && value.Sensitivity != SensitivityPrivate && value.Sensitivity != SensitivitySecret {
			return errors.New("inputmigration: invalid field sensitivity")
		}
	}
	return nil
}

// Validate performs structural and internal consistency checks only. It does
// not prove that the overlay can execute on any particular target device; use
// ValidateForCapabilities when importing onto a known target.
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
	switch o.Backend {
	case "native-imk", "native-tsf", "deferred":
		if o.HookEnabled {
			return errors.New("inputmigration: hook cannot be enabled for non-hook backend")
		}
	case "hook":
		if !o.HookEnabled {
			return errors.New("inputmigration: hook backend requires hook enabled")
		}
	default:
		return errors.New("inputmigration: invalid overlay backend")
	}
	if len(o.InputSourceIDs) == 0 || len(o.InputSourceIDs) > maxCollectionSize || len(o.Shortcuts) > maxCollectionSize || len(o.AppRules) > maxCollectionSize || len(o.DeferredRules) > maxCollectionSize {
		return errors.New("inputmigration: overlay collection too large or empty")
	}
	if err := validateMetadata(o.FieldMetadata); err != nil {
		return err
	}
	if len(o.AccountID) > maxMetadataSize || len(o.DeviceID) > maxMetadataSize || len(o.KeyboardLayout) > maxMetadataSize || len(o.InputSourceID) > maxMetadataSize {
		return errors.New("inputmigration: overlay metadata too long")
	}
	inputSources := make(map[string]struct{}, len(o.InputSourceIDs))
	for _, id := range o.InputSourceIDs {
		if strings.TrimSpace(id) == "" || len(id) > maxMetadataSize {
			return errors.New("inputmigration: invalid overlay input source")
		}
		inputSources[id] = struct{}{}
	}
	if _, ok := inputSources[o.InputSourceID]; !ok {
		return errors.New("inputmigration: selected input source is not available")
	}
	for _, r := range o.Shortcuts {
		if err := validateShortcut(r); err != nil {
			return err
		}
	}
	for _, r := range o.AppRules {
		if err := validateAppRule(r); err != nil {
			return err
		}
		if r.InputSourceID != "" {
			if _, ok := inputSources[r.InputSourceID]; !ok {
				return errors.New("inputmigration: app rule input source is not available")
			}
		}
	}
	if err := validateDisplay(o.Display); err != nil {
		return err
	}
	if len(o.Permissions) > maxMetadataSize {
		return errors.New("inputmigration: too many overlay permissions")
	}
	for key, p := range o.Permissions {
		if key == "" || len(key) > maxMetadataSize || (p != PermissionGranted && p != PermissionDenied && p != PermissionUnknown) {
			return errors.New("inputmigration: invalid overlay permission")
		}
	}
	for _, r := range o.DeferredRules {
		if strings.TrimSpace(r.RuleID) == "" || len(r.RuleID) > maxMetadataSize || strings.TrimSpace(r.Reason) == "" || len(r.Reason) > maxMetadataSize || (r.State != "deferred" && r.State != "unsupported") {
			return errors.New("inputmigration: invalid deferred rule")
		}
	}
	return nil
}

// ValidateForCapabilities validates an overlay against a concrete target. The
// target is deliberately supplied here rather than stored in DeviceOverlay so
// the portable overlay schema does not become tied to one operating system.
func (o DeviceOverlay) ValidateForCapabilities(target DeviceCapabilities) error {
	// Keep the structural validation first: callers get the same schema and
	// account/device/scope errors as Validate, before target policy is applied.
	if err := o.Validate(); err != nil {
		return err
	}
	// Validate intentionally accepts the marker used to preserve unknown v1
	// actions for structural compatibility. It must never be treated as an
	// executable action by a capability-aware import, though: GenerateOverlay
	// turns the same marker into a DeferredRule, so direct overlay imports must
	// report the equivalent deferred state rather than returning nil.
	if err := validateLegacyV1RuleActions(o.AppRules); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if o.KeyboardLayout != target.KeyboardLayout {
		return fmt.Errorf("inputmigration: keyboard layout mismatch: overlay %q, target %q", o.KeyboardLayout, target.KeyboardLayout)
	}

	switch o.Backend {
	case "native-imk":
		if canonicalOS(target.OS) != "darwin" || !target.NativeIMK {
			return errors.New("inputmigration: native-imk backend is incompatible with target capabilities")
		}
	case "native-tsf":
		if canonicalOS(target.OS) != "windows" || !target.NativeTSF {
			return errors.New("inputmigration: native-tsf backend is incompatible with target capabilities")
		}
	case "hook":
		if !target.HookCapability {
			return errors.New("inputmigration: hook backend requires target hook capability")
		}
	case "deferred":
		return fmt.Errorf("%w: backend is deferred", ErrDeferredOverlay)
	}

	targetSources := make(map[string]struct{}, len(target.InputSourceIDs))
	for _, id := range target.InputSourceIDs {
		targetSources[id] = struct{}{}
	}
	if _, ok := targetSources[o.InputSourceID]; !ok {
		return errors.New("inputmigration: selected input source is not available on target")
	}
	// InputSourceIDs can include source-device extras that are not installed on
	// the target. The selected source and every app-rule source are the sources
	// the overlay actually uses, however, and must be available on the target.
	for _, r := range o.AppRules {
		if r.InputSourceID == "" {
			continue
		}
		if _, ok := targetSources[r.InputSourceID]; !ok {
			return errors.New("inputmigration: app rule input source is not available on target")
		}
	}
	return nil
}

func marshalStrict(v any, validate func() error) ([]byte, error) {
	if err := validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(data) > maxJSONSize {
		return nil, errors.New("inputmigration: JSON payload too large")
	}
	return data, nil
}
func unmarshalStrict(data []byte, v any, validate func() error) error {
	if len(data) > maxJSONSize {
		return errors.New("inputmigration: JSON payload too large")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("inputmigration: invalid JSON: %w", err)
	}
	if err := normalizeLegacyPayload(data, v); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("inputmigration: trailing JSON")
		}
		return fmt.Errorf("inputmigration: trailing JSON: %w", err)
	}
	return nil
}

// rejectDuplicateKeys walks JSON tokens instead of decoding objects into maps.
// This preserves the normal struct decoder's type and unknown-field checks,
// while making duplicate keys (including nested keys) an explicit error.
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(dec); err != nil {
		return fmt.Errorf("inputmigration: invalid JSON: %w", err)
	}
	return nil
}

func scanJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch delimiter := tok.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return errors.New("object key is not a string")
				}
				if _, exists := seen[name]; exists {
					return fmt.Errorf("duplicate JSON object key %q", name)
				}
				seen[name] = struct{}{}
				if err := scanJSONValue(dec); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				if err != nil {
					return err
				}
				return errors.New("object did not end")
			}
		case '[':
			for dec.More() {
				if err := scanJSONValue(dec); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				if err != nil {
					return err
				}
				return errors.New("array did not end")
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
	}
	return nil
}

func normalizeLegacyPayload(data []byte, v any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("inputmigration: invalid JSON object: %w", err)
	}
	var version uint16
	if raw, ok := fields["schemaVersion"]; ok {
		if err := json.Unmarshal(raw, &version); err != nil {
			return nil // the strict struct decoder reports the useful type error
		}
	}
	if version != SchemaVersion {
		return nil
	}
	normalizeBaseline := func(b *PersonalBaseline, object map[string]json.RawMessage) {
		if raw, ok := object["scheme"]; ok && !validScheme(b.Scheme) && b.Scheme != "" {
			b.legacyV1Scheme = b.Scheme
			_ = raw
		}
		if raw, ok := object["learning"]; ok {
			var learning map[string]json.RawMessage
			if json.Unmarshal(raw, &learning) == nil {
				if _, present := learning["summaryOnly"]; !present && (len(b.Learning.Words) > 0 || len(b.Learning.Bigrams) > 0 || len(b.Learning.Aggregates) > 0) {
					b.Learning.SummaryOnly = true
				}
			}
		}
		normalizeRuleActions(b.AppRules, object["appRules"])
	}
	switch x := v.(type) {
	case *PersonalBaseline:
		normalizeBaseline(x, fields)
	case *DeviceOverlay:
		normalizeRuleActions(x.AppRules, fields["appRules"])
	case *Export:
		var baselineFields map[string]json.RawMessage
		if raw, ok := fields["baseline"]; ok && json.Unmarshal(raw, &baselineFields) == nil {
			normalizeBaseline(&x.Baseline, baselineFields)
		}
		if x.Overlay != nil {
			var overlayFields map[string]json.RawMessage
			if raw, ok := fields["overlay"]; ok && json.Unmarshal(raw, &overlayFields) == nil {
				normalizeRuleActions(x.Overlay.AppRules, overlayFields["appRules"])
			}
		}
	}
	return nil
}

func normalizeRuleActions(rules []AppRule, raw json.RawMessage) {
	if len(rules) == 0 || len(raw) == 0 {
		return
	}
	var encoded []json.RawMessage
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	for i := range rules {
		if i >= len(encoded) || rules[i].Action == "" || rules[i].Action == "ascii" || rules[i].Action == "native" || rules[i].Action == "keep" || strings.HasPrefix(rules[i].Action, "scheme:") {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(encoded[i], &fields) == nil {
			if _, present := fields["action"]; present {
				rules[i].legacyV1Action = rules[i].Action
			}
		}
	}
}
func MarshalBaseline(b PersonalBaseline) ([]byte, error) { return marshalStrict(b, b.Validate) }
func UnmarshalBaseline(data []byte) (PersonalBaseline, error) {
	var b PersonalBaseline
	return b, unmarshalStrict(data, &b, func() error { return b.Validate() })
}
func MarshalOverlay(o DeviceOverlay) ([]byte, error) { return marshalStrict(o, o.Validate) }

// UnmarshalOverlay performs strict structural parsing and v1 compatibility
// normalization only. Its result is not proven executable; callers importing
// an overlay onto a target must use UnmarshalOverlayForCapabilities first.
func UnmarshalOverlay(data []byte) (DeviceOverlay, error) {
	var o DeviceOverlay
	return o, unmarshalStrict(data, &o, func() error { return o.Validate() })
}

// UnmarshalOverlayForCapabilities strictly parses and validates an overlay,
// then checks whether it is executable on target. On every error, including
// ErrDeferredOverlay, the returned overlay contains the successfully decoded
// object when schema validation succeeded so callers can inspect and explain
// why execution was deferred.
func UnmarshalOverlayForCapabilities(data []byte, target DeviceCapabilities) (DeviceOverlay, error) {
	var o DeviceOverlay
	if err := unmarshalStrict(data, &o, func() error { return o.Validate() }); err != nil {
		return o, err
	}
	return o, o.ValidateForCapabilities(target)
}

func MarshalExport(e Export) ([]byte, error) { return marshalStrict(e, e.Validate) }

// UnmarshalExport performs strict structural parsing and v1 compatibility
// normalization only. Its result is not proven executable; callers importing
// an export with an overlay must use UnmarshalExportForCapabilities before
// applying that overlay.
func UnmarshalExport(data []byte) (Export, error) {
	var e Export
	return e, unmarshalStrict(data, &e, func() error { return e.Validate() })
}

// UnmarshalExportForCapabilities strictly parses and validates the baseline
// and optional overlay. A baseline-only export is portable without a complete
// target capability snapshot, so target validation is skipped when Overlay is
// nil. With an overlay, capability validation is required. As with
// UnmarshalOverlayForCapabilities, a successfully decoded export is returned
// even when its overlay is deferred or otherwise not executable, allowing
// callers to inspect and present the reason.
func UnmarshalExportForCapabilities(data []byte, target DeviceCapabilities) (Export, error) {
	var e Export
	if err := unmarshalStrict(data, &e, func() error { return e.Validate() }); err != nil {
		return e, err
	}
	if e.Overlay == nil {
		return e, nil
	}
	if err := e.Overlay.ValidateForCapabilities(target); err != nil {
		return e, err
	}
	return e, nil
}

// Validate performs structural and cross-layer consistency checks only. It
// cannot prove that the optional overlay is compatible with a target device;
// use UnmarshalExportForCapabilities (or ValidateForCapabilities) at import.
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
	out.legacyV1Scheme = b.legacyV1Scheme
	for i := range out.AppRules {
		if i < len(b.AppRules) {
			out.AppRules[i].legacyV1Action = b.AppRules[i].legacyV1Action
		}
	}
	return out
}
func cloneOverlay(o DeviceOverlay) DeviceOverlay {
	raw, _ := json.Marshal(o)
	var out DeviceOverlay
	_ = json.Unmarshal(raw, &out)
	for i := range out.AppRules {
		if i < len(o.AppRules) {
			out.AppRules[i].legacyV1Action = o.AppRules[i].legacyV1Action
		}
	}
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
	// Validation accepts harmless surrounding whitespace for compatibility, but
	// generation uses and retains one canonical spelling for every platform
	// decision. This prevents a valid value such as " windows " from falling
	// through backend selection.
	target.OS = canonicalOS(target.OS)
	if strings.TrimSpace(deviceID) == "" {
		return Preview{}, errors.New("inputmigration: device id is required")
	}
	o := NewDeviceOverlay(b.AccountID, deviceID)
	o.KeyboardLayout, o.InputSourceIDs, o.InputSourceID = target.KeyboardLayout, append([]string(nil), target.InputSourceIDs...), target.InputSourceIDs[0]
	o.Permissions, o.Display = copyPermissions(target.Permissions), target.Display
	o.Backend = backendFor(target)
	o.HookEnabled = target.HookCapability && o.Backend == "hook"
	o.Shortcuts = cloneShortcuts(b.Shortcuts)
	if b.legacyV1Scheme != "" && b.Scheme == b.legacyV1Scheme {
		o.DeferredRules = append(o.DeferredRules, DeferredRule{
			RuleID: "scheme",
			State:  "deferred",
			Reason: "legacy scheme " + safeSummary(b.legacyV1Scheme) + " is unsupported",
		})
	}
	for _, r := range b.AppRules {
		if r.legacyV1Action != "" && r.Action == r.legacyV1Action {
			o.DeferredRules = append(o.DeferredRules, DeferredRule{RuleID: r.ID, State: "deferred", Reason: "legacy action " + safeSummary(r.Action) + " is unsupported"})
			continue
		}
		if r.InputSourceID != "" && !containsString(target.InputSourceIDs, r.InputSourceID) {
			o.DeferredRules = append(o.DeferredRules, DeferredRule{
				RuleID: r.ID,
				State:  "unsupported",
				Reason: "target lacks input source " + safeSummary(r.InputSourceID),
			})
			continue
		}
		o.AppRules = append(o.AppRules, cloneAppRules([]AppRule{r})...)
	}
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
		if normalizeOS(b.SourceDevice.OS) != normalizeOS(target.OS) {
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
		path := "/shortcuts/" + safePathSegment(r.RuleID)
		if strings.HasPrefix(r.Reason, "target lacks input source") || strings.HasPrefix(r.Reason, "legacy action") {
			path = "/app-rules/" + safePathSegment(r.RuleID)
		}
		add(path, CategoryAppRule, "supported", r.State, r.Reason)
	}
	return p, nil
}
func normalizeOS(os string) string {
	if canonical := canonicalOS(os); canonical != "" {
		return canonical
	}
	return strings.ToLower(strings.TrimSpace(os))
}
func safePathSegment(v string) string {
	if v == "" {
		return "empty"
	}
	return digest(v)[:16]
}
func backendFor(c DeviceCapabilities) string {
	switch canonicalOS(c.OS) {
	case "windows":
		if c.NativeTSF {
			return "native-tsf"
		}
	case "darwin":
		if c.NativeIMK {
			return "native-imk"
		}
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
	if c == nil || len(c.Permissions) == 0 {
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
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
	out.FieldRevisions = copyRevisions(source.FieldRevisions)
	conflicts := []FieldConflict{}
	choose := func(path string, different bool, apply func()) {
		sr, tr := source.FieldRevisions[path], target.FieldRevisions[path]
		if !different {
			out.FieldRevisions[path] = max(sr, tr)
			return
		}
		if tr > sr {
			// A newer target is already the result of a previous import. Do not
			// manufacture a revision on every replay of an older source.
			apply()
			out.FieldRevisions[path] = tr
			return
		}
		if tr == sr {
			conflicts = append(conflicts, FieldConflict{Path: path, Source: "source", Target: "target", Winner: "target"})
			apply()
			// Equal-revision conflicts advance once; replaying the resulting
			// baseline sees the newer revision and therefore converges.
			out.FieldRevisions[path] = max(sr, tr) + 1
			return
		}
		// Source is newer and remains the winner. Keeping its revision avoids
		// manufacturing a new revision each time an older export is replayed.
		out.FieldRevisions[path] = sr
	}
	choose("scheme", source.Scheme != target.Scheme, func() { out.Scheme = target.Scheme })
	choose("candidateCount", source.CandidateCount != target.CandidateCount, func() { out.CandidateCount = target.CandidateCount })
	choose("shiftToggleASCII", source.ShiftToggleASCII != target.ShiftToggleASCII, func() { out.ShiftToggleASCII = target.ShiftToggleASCII })
	choose("fuzzyPinyin", !sameStringSet(source.FuzzyPinyin, target.FuzzyPinyin), func() { out.FuzzyPinyin = append([]string(nil), target.FuzzyPinyin...) })
	choose("dictionaries", !sameJSON(source.Dictionaries, target.Dictionaries), func() { out.Dictionaries = mergeDictionaries(source, target, &conflicts) })
	choose("learning", !sameLearning(source.Learning, target.Learning), func() { out.Learning = cloneLearning(target.Learning) })
	choose("shortcuts", !sameJSON(source.Shortcuts, target.Shortcuts), func() { out.Shortcuts = cloneShortcuts(target.Shortcuts) })
	choose("appRules", !sameJSON(source.AppRules, target.AppRules), func() { out.AppRules = cloneAppRules(target.AppRules) })
	return out, conflicts, out.Validate()
}
func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
func copyRevisions(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneShortcuts(in []ShortcutRule) []ShortcutRule {
	out := make([]ShortcutRule, len(in))
	for i, r := range in {
		out[i] = r
		out[i].Apps = append([]StableApp(nil), r.Apps...)
	}
	return out
}
func cloneAppRules(in []AppRule) []AppRule {
	out := make([]AppRule, len(in))
	for i, r := range in {
		out[i] = r
		out[i].Apps = append([]StableApp(nil), r.Apps...)
	}
	return out
}
func mergeDictionaries(source, target PersonalBaseline, conflicts *[]FieldConflict) []DictionaryMetadata {
	byID := map[string]DictionaryMetadata{}
	for _, d := range source.Dictionaries {
		byID[d.ID] = d
	}
	for _, d := range target.Dictionaries {
		if old, ok := byID[d.ID]; ok && old.ContentHash != d.ContentHash {
			winner := old
			winnerName := "source"
			if d.Revision >= old.Revision {
				winner, winnerName = d, "target"
			}
			*conflicts = append(*conflicts, FieldConflict{Path: "/dictionaries/" + safePathSegment(d.ID), Source: old.ContentHash, Target: d.ContentHash, Winner: winnerName})
			byID[d.ID] = winner
			continue
		}
		// Same ID/content is an idempotent import; newer metadata is preferred.
		if old, ok := byID[d.ID]; !ok || d.Revision >= old.Revision {
			byID[d.ID] = d
		}
	}
	// A content hash identifies the dictionary payload even when two stores
	// assigned different IDs. Keep one deterministic representative.
	byHash := map[string]DictionaryMetadata{}
	for _, d := range byID {
		if old, ok := byHash[d.ContentHash]; !ok || d.ID < old.ID {
			byHash[d.ContentHash] = d
		}
	}
	out := make([]DictionaryMetadata, 0, len(byHash))
	for _, d := range byHash {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].ContentHash < out[j].ContentHash
	})
	return out
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
