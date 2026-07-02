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
}
