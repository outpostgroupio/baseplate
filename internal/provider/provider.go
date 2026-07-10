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
)

func (a ChangeAction) String() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	case ActionDelete:
		return "delete"
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

// Sensitive is an optional Resource capability. Sensitive returns attribute names
// whose values must be redacted from all rendered plan output (they may be secrets
// sourced from env()). The values still flow to the provider at apply; only
// rendering is affected. State never contains them because such resources omit
// them from the attributes they return (see roblox_universe_secret).
type Sensitive interface {
	Sensitive() []string
}
