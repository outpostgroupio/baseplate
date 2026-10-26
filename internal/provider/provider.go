package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
)

var ErrNotFound = errors.New("resource not found")

// Attributes is a resource's fields keyed by attribute name.
type Attributes map[string]cty.Value

// Ref is a dependency on another resource's attribute, e.g.
// {Address: "mock_thing.a", Attr: "id"}.
type Ref struct {
	Address string
	Attr    string
}

type ChangeAction int

const (
	ActionNoop ChangeAction = iota
	ActionCreate
	ActionUpdate
	ActionDelete
	// ActionReplace creates a new remote object and then deletes the old one
	// (create-before-destroy), because a change the provider can't apply in
	// place forces a new object with a new id. See Replacer.
	ActionReplace
)

func (a ChangeAction) String() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	case ActionDelete:
		return "delete"
	case ActionReplace:
		return "replace"
	default:
		return "noop"
	}
}

type Provider interface {
	// Resources maps a resource type name (e.g. "mock_thing") to its impl.
	Resources() map[string]Resource
}

type Resource interface {
	Type() string
	DecodeSpec() hcldec.Spec
	Read(ctx context.Context, id string) (Attributes, error)
	Create(ctx context.Context, desired Attributes) (id string, out Attributes, err error)
	Update(ctx context.Context, id string, desired Attributes) (Attributes, error)
	Delete(ctx context.Context, id string) error
	Dependencies(desired Attributes) []Ref
	// Computed returns attribute names that are set by the provider (IDs,
	// timestamps, etc.) and must be excluded from desired-vs-actual diffing.
	Computed() []string
}

// Triggerable is an optional Resource capability. Triggers returns values
// computed from the resolved desired config that force an Update when any of
// them changes, independent of the desired-vs-actual attribute diff. Used for
// opaque uploads (e.g. a place file's content hash) that can't be read back and
// compared. The values are persisted in state and compared on plan.
type Triggerable interface {
	Triggers(desired Attributes) (map[string]cty.Value, error)
}

// Replacer is an optional Resource capability, Baseplate's equivalent of
// Terraform's "forces replacement". RequiresReplace returns the attribute and/or
// trigger names (see Triggerable) whose change can't be applied in place: when
// any of them differs, plan calls for a replace (a new remote object with a new
// id) instead of an update. It receives the desired attributes and the actual
// ones (as Read reports them) so the answer can depend on the object's current
// state (e.g. a Model asset takes new content in place, an Image needs a new
// upload). Desired values may be unknown (a reference to a resource being
// replaced).
//
// Only names that the plan diffs can force a replace: declared attributes that
// are not in Computed (a Normalizer can fill in an omitted optional's default
// so it is diffed), and trigger keys. A replace-forcing trigger with no
// recorded value in state (state written before the trigger existed) is adopted
// as the baseline instead of forcing a replace; apply records it.
//
// ReplaceAbandons reports what happens to the old object: false deletes it
// (Delete) once its successor exists; true leaves it in place on the platform
// and only drops it from state, with no remote call. Abandon suits objects that
// running games may still use by id (e.g. an asset that live servers load as
// rbxassetid://<old id> until the place is republished).
type Replacer interface {
	RequiresReplace(desired, actual Attributes) []string
	ReplaceAbandons() bool
}

// TriggerUnavailableError is returned by Triggerable.Triggers when an input a
// trigger is computed from is missing (e.g. a source file not present on this
// machine, as in a CI checkout without the images). Triggers may return the
// triggers it could still compute alongside it. For a resource already in
// state, plan keeps the recorded values of the others (no change detected) and
// warns; a create still fails, since it needs the input.
type TriggerUnavailableError struct {
	Reason string // e.g. `file "a.png" not found`
}

func (e *TriggerUnavailableError) Error() string { return e.Reason }

// TriggerAwareUpdater is an optional Resource capability: when present, apply
// calls UpdateWithTriggers instead of Update, passing the triggers recorded in
// state before this update (decoded; empty when there are none). Comparing
// them with the triggers of the desired config tells the resource which input
// changed, so it can skip work for the ones that didn't (e.g. not re-upload an
// unchanged file on a metadata-only edit).
type TriggerAwareUpdater interface {
	UpdateWithTriggers(ctx context.Context, id string, desired Attributes, prior map[string]cty.Value) (Attributes, error)
}

// Sensitive is an optional Resource capability. Sensitive returns attribute names
// whose values must be redacted from all rendered plan output (they may be secrets
// sourced from env()). The values still flow to the provider at apply; only
// rendering is affected. State never contains them because such resources omit
// them from the attributes they return (see roblox_universe_secret).
type Sensitive interface {
	Sensitive() []string
}

// Normalizer is an optional Resource capability. Normalize returns the desired
// attributes rewritten into the canonical form Read reports (e.g. a timestamp
// converted to UTC at a fixed precision), so semantically equal values don't
// produce a perpetual diff. It may also reject an invalid value, which surfaces
// the error at plan time instead of at apply. Implementations must leave null
// and unknown values untouched and must not mutate the input map.
type Normalizer interface {
	Normalize(desired Attributes) (Attributes, error)
}

// Documented is an optional Resource capability. Doc returns authored
// documentation for a resource — its summary, per-attribute descriptions, any
// caveats, and the API-key scopes it needs. The resource-docs generator merges
// this with the schema-derived structure (types + kind tags) to render the
// resource reference. Descriptions live here, next to DecodeSpec, so a missing
// one fails generation rather than drifting.
type Documented interface {
	Doc() ResourceDoc
}

// ResourceDoc is the authored documentation for one resource.
type ResourceDoc struct {
	Summary     string            // 1–2 sentence resource description
	Attributes  map[string]string // per-attribute description, keyed by DecodeSpec attr name
	Notes       string            // optional caveats paragraph (destroy behavior, read-only fields…)
	Scopes      []string          // e.g. []string{"universe:write"}; rendered + linked to api-key-scopes.md
	ExampleFile string            // optional; repo-relative path to an example, rendered as a link
	// TriggerAttrs names attributes whose changes are detected via a trigger and
	// force an update (rendered with the "trigger" kind instead of "create-only").
	TriggerAttrs []string
}
