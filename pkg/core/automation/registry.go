// Package automation contains the provider-neutral registry used to discover
// actions and workflows. This file intentionally depends on config only; it
// must remain usable by the core and must not import UI or controller code.
package automation

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
)

// DescriptorKind identifies the source of a registry descriptor.
type DescriptorKind string

const (
	KindBuiltin  DescriptorKind = "builtin"
	KindScript   DescriptorKind = "script"
	KindWorkflow DescriptorKind = "workflow"

	// The aliases are kept for callers that use the noun form.
	DescriptorBuiltin  = KindBuiltin
	DescriptorScript   = KindScript
	DescriptorWorkflow = KindWorkflow
)

const (
	registryProviderBuiltin  = "builtin"
	registryProviderScript   = "script"
	registryProviderWorkflow = "workflow"
)

// ProviderAvailability is the current availability of a provider. Reason is
// deliberately a plain string so providers can report a useful explanation
// without coupling the registry to an error or UI type.
type ProviderAvailability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// ProviderStatus is a descriptive alias for ProviderAvailability.
type ProviderStatus = ProviderAvailability

// Descriptor is the small, serializable registry-facing view of an
// automation. Payloads retain the source config values needed by core
// execution, but no executable callbacks are stored here.
//
// This is a local compatibility type. The workflow foundation currently lives
// in a sibling change; once that change is present, Descriptor should be
// reconciled with the shared automation ActionDescriptor rather than creating
// a second public model.
type Descriptor struct {
	ID           string                      `json:"id"`
	Name         string                      `json:"name,omitempty"`
	Description  string                      `json:"description,omitempty"`
	Kind         DescriptorKind              `json:"kind"`
	Provider     string                      `json:"provider"`
	Availability ProviderAvailability        `json:"availability"`
	OutputSchema map[string]any              `json:"outputSchema,omitempty"`
	Action       *config.LeaderAction        `json:"action,omitempty"`
	Script       *config.ScriptComponentSpec `json:"script,omitempty"`
	Workflow     any                         `json:"workflow,omitempty"`
}

// BuiltinDescriptor is the input form for a builtin action registration.
type BuiltinDescriptor struct {
	ID          string
	Name        string
	Description string
	Action      config.LeaderAction
}

// ScriptDescriptor is the input form for a script registration. Spec is kept
// as a value so callers cannot mutate a descriptor by changing their input
// slice after registration.
type ScriptDescriptor struct {
	Spec        config.ScriptComponentSpec
	Name        string
	Description string
}

// WorkflowDescriptor is intentionally payload-agnostic. It can wrap the
// workflow config type when that type is available, while this package remains
// independent of its persistence representation.
type WorkflowDescriptor struct {
	ID          string
	Name        string
	Description string
	Workflow    any
}

var (
	// ErrDuplicateID indicates that a registration would replace an existing
	// descriptor. Registrations are never implicitly overwritten.
	ErrDuplicateID = errors.New("automation registry: duplicate descriptor id")
	ErrInvalidID   = errors.New("automation registry: descriptor id is required")
)

// Registry is a deterministic, in-memory descriptor registry. A Registry is
// safe for setup-time use; callers should finish registration before sharing
// a registry between execution goroutines.
type Registry struct {
	descriptors map[string]Descriptor
	providers   map[string]ProviderAvailability
}

// NewRegistry creates an empty registry with the three built-in provider
// families available. Unknown providers are treated as available until an
// explicit status is recorded for them.
func NewRegistry() *Registry {
	r := &Registry{
		descriptors: make(map[string]Descriptor),
		providers:   make(map[string]ProviderAvailability),
	}
	for _, provider := range []string{registryProviderBuiltin, registryProviderScript, registryProviderWorkflow} {
		r.providers[provider] = ProviderAvailability{Available: true}
	}
	return r
}

// Register adds one descriptor. IDs are global across descriptor kinds: a
// script cannot shadow a builtin or workflow with the same ID.
func (r *Registry) Register(descriptor Descriptor) error {
	if r == nil {
		return errors.New("automation registry: nil registry")
	}
	descriptor.ID = strings.TrimSpace(descriptor.ID)
	if descriptor.ID == "" {
		return ErrInvalidID
	}
	if r.descriptors == nil {
		r.descriptors = make(map[string]Descriptor)
	}
	if r.providers == nil {
		r.providers = make(map[string]ProviderAvailability)
	}
	if _, exists := r.descriptors[descriptor.ID]; exists {
		return fmt.Errorf("%w %q", ErrDuplicateID, descriptor.ID)
	}
	if descriptor.Provider == "" {
		descriptor.Provider = string(descriptor.Kind)
	}
	if status, ok := r.providers[descriptor.Provider]; ok {
		descriptor.Availability = status
	} else if descriptor.Availability.Reason != "" || descriptor.Availability.Available {
		// Preserve an explicitly supplied status for a provider not yet known to
		// the registry, and make it visible to future registrations.
		r.providers[descriptor.Provider] = descriptor.Availability
	} else {
		descriptor.Availability.Available = true
	}
	descriptor = cloneDescriptor(descriptor)
	r.descriptors[descriptor.ID] = descriptor
	return nil
}

// RegisterDescriptor is an explicit-name alias for Register.
func (r *Registry) RegisterDescriptor(descriptor Descriptor) error { return r.Register(descriptor) }

// RegisterAction is an alias for registering a generic descriptor. It is useful
// when callers use "action" as the domain term for builtin or script entries.
func (r *Registry) RegisterAction(descriptor Descriptor) error { return r.Register(descriptor) }

// RegisterBuiltin registers a builtin action. The flexible input accepts
// BuiltinDescriptor, Descriptor, or (string, config.LeaderAction), allowing
// callers to migrate without a UI/controller adapter.
func (r *Registry) RegisterBuiltin(value any, action ...config.LeaderAction) error {
	var descriptor BuiltinDescriptor
	switch value := value.(type) {
	case BuiltinDescriptor:
		descriptor = value
	case Descriptor:
		value.Kind = KindBuiltin
		if value.Provider == "" {
			value.Provider = registryProviderBuiltin
		}
		return r.Register(value)
	case string:
		descriptor.ID = value
		if len(action) > 0 {
			descriptor.Action = action[0]
		}
	default:
		if adapted, ok := adaptDescriptor(value, KindBuiltin, registryProviderBuiltin); ok {
			return r.Register(adapted)
		}
		return fmt.Errorf("automation registry: unsupported builtin descriptor %T", value)
	}
	return r.Register(Descriptor{
		ID: descriptor.ID, Name: descriptor.Name, Description: descriptor.Description,
		Kind: KindBuiltin, Provider: registryProviderBuiltin, Action: cloneActionPtr(descriptor.Action),
	})
}

// RegisterScript registers a config.ScriptComponentSpec, ScriptDescriptor, or
// already-shaped Descriptor.
func (r *Registry) RegisterScript(value any, spec ...config.ScriptComponentSpec) error {
	var descriptor ScriptDescriptor
	if len(spec) > 0 {
		id, ok := value.(string)
		if !ok {
			return fmt.Errorf("automation registry: script ID must be a string, got %T", value)
		}
		descriptor.Spec = spec[0]
		descriptor.Spec.ID = id
	} else {
		switch value := value.(type) {
		case config.ScriptComponentSpec:
			descriptor.Spec = value
		case ScriptDescriptor:
			descriptor = value
		case Descriptor:
			value.Kind = KindScript
			if value.Provider == "" {
				value.Provider = registryProviderScript
			}
			return r.Register(value)
		default:
			return fmt.Errorf("automation registry: unsupported script descriptor %T", value)
		}
	}
	if strings.TrimSpace(descriptor.Spec.ID) == "" {
		return ErrInvalidID
	}
	return r.Register(Descriptor{
		ID: descriptor.Spec.ID, Name: firstNonEmpty(descriptor.Name, descriptor.Spec.Title),
		Description: firstNonEmpty(descriptor.Description, descriptor.Spec.Subtitle),
		Kind:        KindScript, Provider: registryProviderScript, Script: cloneScriptPtr(descriptor.Spec),
	})
}

// RegisterWorkflow registers a provider-neutral workflow descriptor or a
// Descriptor. The workflow payload is not interpreted by this package.
func (r *Registry) RegisterWorkflow(value any, workflow ...any) error {
	if len(workflow) > 0 {
		id, ok := value.(string)
		if !ok {
			return fmt.Errorf("automation registry: workflow ID must be a string, got %T", value)
		}
		return r.Register(Descriptor{ID: id, Kind: KindWorkflow, Provider: registryProviderWorkflow, Workflow: workflow[0]})
	}
	switch value := value.(type) {
	case WorkflowDescriptor:
		return r.Register(Descriptor{ID: value.ID, Name: value.Name, Description: value.Description,
			Kind: KindWorkflow, Provider: registryProviderWorkflow, Workflow: value.Workflow})
	case Descriptor:
		value.Kind = KindWorkflow
		if value.Provider == "" {
			value.Provider = registryProviderWorkflow
		}
		return r.Register(value)
	default:
		if adapted, ok := adaptDescriptor(value, KindWorkflow, registryProviderWorkflow); ok {
			return r.Register(adapted)
		}
		return fmt.Errorf("automation registry: unsupported workflow descriptor %T", value)
	}
}

// SetProviderAvailability records provider state and updates all descriptors
// already registered for that provider. status may be a ProviderAvailability
// value or a bool followed by an optional reason; the latter form keeps the
// adapter compatible with small provider probes that return three values.
func (r *Registry) SetProviderAvailability(provider string, value any, reason ...string) error {
	if r == nil {
		return errors.New("automation registry: nil registry")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return errors.New("automation registry: provider is required")
	}
	var status ProviderAvailability
	switch value := value.(type) {
	case ProviderAvailability:
		status = value
	case bool:
		status = ProviderAvailability{Available: value}
		if len(reason) > 0 {
			status.Reason = reason[0]
		}
	default:
		return fmt.Errorf("automation registry: unsupported provider status %T", value)
	}
	if !status.Available {
		status.Reason = strings.TrimSpace(status.Reason)
	} else {
		status.Reason = ""
	}
	if r.providers == nil {
		r.providers = make(map[string]ProviderAvailability)
	}
	r.providers[provider] = status
	for id, descriptor := range r.descriptors {
		if descriptor.Provider == provider {
			descriptor.Availability = status
			r.descriptors[id] = descriptor
		}
	}
	return nil
}

// SetProviderStatus is a convenience form for callers with separate fields.
func (r *Registry) SetProviderStatus(provider string, available bool, reason string) error {
	return r.SetProviderAvailability(provider, available, reason)
}

// ProviderAvailability returns the current provider status. An unrecorded
// provider is considered available, which keeps adding a new provider
// non-breaking while still allowing explicit unavailability.
func (r *Registry) ProviderAvailability(provider string) ProviderAvailability {
	if r == nil {
		return ProviderAvailability{Reason: "registry is nil"}
	}
	if status, ok := r.providers[provider]; ok {
		return status
	}
	return ProviderAvailability{Available: true}
}

// List returns descriptors sorted by ID. Returned descriptors are copies.
func (r *Registry) List() []Descriptor {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.descriptors))
	for id := range r.descriptors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Descriptor, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneDescriptor(r.descriptors[id]))
	}
	return out
}

// Get retrieves a descriptor by ID. Returned descriptors are copies.
func (r *Registry) Get(id string) (Descriptor, bool) {
	if r == nil {
		return Descriptor{}, false
	}
	descriptor, ok := r.descriptors[id]
	if !ok {
		return Descriptor{}, false
	}
	return cloneDescriptor(descriptor), true
}

// Lookup is an alias for Get.
func (r *Registry) Lookup(id string) (Descriptor, bool) { return r.Get(id) }

// GetDescriptor is an explicit-name alias for Get.
func (r *Registry) GetDescriptor(id string) (Descriptor, bool) { return r.Get(id) }

func cloneDescriptor(descriptor Descriptor) Descriptor {
	if descriptor.OutputSchema != nil {
		descriptor.OutputSchema = cloneValue(descriptor.OutputSchema).(map[string]any)
	}
	descriptor.Action = cloneActionPtrValue(descriptor.Action)
	descriptor.Script = cloneScriptPtrValue(descriptor.Script)
	return descriptor
}

func cloneActionPtr(action config.LeaderAction) *config.LeaderAction {
	return cloneActionPtrValue(&action)
}

func cloneActionPtrValue(action *config.LeaderAction) *config.LeaderAction {
	if action == nil {
		return nil
	}
	copy := *action
	copy.Args = append([]string(nil), action.Args...)
	copy.Children = make([]config.LeaderNode, len(action.Children))
	for i, child := range action.Children {
		copy.Children[i] = child
		copy.Children[i].Action = *cloneActionPtrValue(&child.Action)
	}
	return &copy
}

func cloneScriptPtr(script config.ScriptComponentSpec) *config.ScriptComponentSpec {
	return cloneScriptPtrValue(&script)
}

// OutputSchema implements SchemaResolver for registry descriptors.
func (r *Registry) OutputSchema(actionID string) (map[string]any, bool) {
	if r == nil {
		return nil, false
	}
	descriptor, ok := r.Get(actionID)
	if !ok || descriptor.OutputSchema == nil {
		return nil, false
	}
	return cloneValue(descriptor.OutputSchema).(map[string]any), true
}

// NewRegistrySchemaResolver adapts any ActionRegistry, including Registry and
// MapActionRegistry, to the runner's output-schema resolver seam.
func NewRegistrySchemaResolver(registry ActionRegistry) SchemaResolver {
	if resolver, ok := registry.(SchemaResolver); ok {
		return resolver
	}
	return SchemaResolverFunc(func(actionID string) (map[string]any, bool) {
		descriptor, ok := lookupAction(registry, actionID)
		if !ok || descriptor.OutputSchema == nil {
			return nil, false
		}
		return cloneValue(descriptor.OutputSchema).(map[string]any), true
	})
}

func cloneScriptPtrValue(script *config.ScriptComponentSpec) *config.ScriptComponentSpec {
	if script == nil {
		return nil
	}
	copy := *script
	copy.Args = append([]string(nil), script.Args...)
	return &copy
}

// NewBuiltinDescriptor adapts a config leader action without importing a UI
// package. The action is copied when it is registered.
func NewBuiltinDescriptor(id string, action config.LeaderAction) Descriptor {
	return Descriptor{ID: id, Kind: KindBuiltin, Provider: registryProviderBuiltin, Action: cloneActionPtr(action)}
}

// NewScriptDescriptor adapts a config script component without importing a UI
// package. The spec is copied when it is registered.
func NewScriptDescriptor(spec config.ScriptComponentSpec) Descriptor {
	return Descriptor{ID: spec.ID, Name: spec.Title, Description: spec.Subtitle,
		Kind: KindScript, Provider: registryProviderScript, Script: cloneScriptPtr(spec)}
}

// NewWorkflowDescriptor adapts an arbitrary workflow domain value. It is
// intentionally any until the workflow model is unified with this package.
func NewWorkflowDescriptor(id string, workflow any) Descriptor {
	return Descriptor{ID: id, Kind: KindWorkflow, Provider: registryProviderWorkflow, Workflow: workflow}
}

// adaptDescriptor intentionally uses structural reflection for the workflow
// and action descriptor types that may be supplied by a sibling foundation
// change. This keeps this file buildable at the base commit while allowing the
// parent to pass those types without another UI-facing adapter.
func adaptDescriptor(value any, kind DescriptorKind, provider string) (Descriptor, bool) {
	v := reflect.ValueOf(value)
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return Descriptor{}, false
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return Descriptor{}, false
	}
	fieldString := func(name string) string {
		field := v.FieldByName(name)
		if field.IsValid() && field.Kind() == reflect.String {
			return field.String()
		}
		return ""
	}
	id := fieldString("ID")
	if strings.TrimSpace(id) == "" {
		return Descriptor{}, false
	}
	descriptor := Descriptor{
		ID: id, Name: fieldString("Name"), Description: fieldString("Description"),
		Kind: kind, Provider: firstNonEmpty(fieldString("Provider"), provider), Workflow: value,
	}
	if field := v.FieldByName("OutputSchema"); field.IsValid() && field.CanInterface() && field.Type() == reflect.TypeOf(map[string]any{}) && !field.IsNil() {
		descriptor.OutputSchema = cloneValue(field.Interface()).(map[string]any)
	}
	return descriptor, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
