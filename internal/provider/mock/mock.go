package mock

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
)

type Provider struct {
	mu    sync.Mutex
	store map[string]provider.Attributes
	seq   int
}

func New() *Provider {
	return &Provider{store: map[string]provider.Attributes{}}
}

func (p *Provider) Seed(id string, a provider.Attributes) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.store[id] = clone(a)
}

// Store returns a deep copy of the remote store for test assertions, so callers
// cannot mutate provider state through the returned map.
func (p *Provider) Store() map[string]provider.Attributes {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]provider.Attributes, len(p.store))
	for id, a := range p.store {
		out[id] = clone(a)
	}
	return out
}

func (p *Provider) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{
		"mock_thing":    &res{p: p, typ: "mock_thing"},
		"mock_child":    &res{p: p, typ: "mock_child"},
		"mock_computed": &res{p: p, typ: "mock_computed"},
	}
}

type res struct {
	p   *Provider
	typ string
}

func (r *res) Type() string { return r.typ }

func (r *res) DecodeSpec() hcldec.Spec {
	spec := hcldec.ObjectSpec{
		"name": &hcldec.AttrSpec{Name: "name", Type: cty.String, Required: true},
	}
	if r.typ == "mock_child" {
		spec["parent"] = &hcldec.AttrSpec{Name: "parent", Type: cty.String, Required: false}
	}
	return spec
}

func (r *res) Read(_ context.Context, id string) (provider.Attributes, error) {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	a, ok := r.p.store[id]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return clone(a), nil
}

func (r *res) Create(_ context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	r.p.seq++
	id := fmt.Sprintf("%s/%d", r.typ, r.p.seq)
	r.p.store[id] = clone(desired)
	return id, clone(desired), nil
}

func (r *res) Update(_ context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	r.p.store[id] = clone(desired)
	return clone(desired), nil
}

func (r *res) Delete(_ context.Context, id string) error {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	delete(r.p.store, id)
	return nil
}

func (r *res) Computed() []string {
	if r.typ == "mock_computed" {
		return []string{"server_id"}
	}
	return nil
}

func (r *res) Dependencies(desired provider.Attributes) []provider.Ref {
	if r.typ != "mock_child" {
		return nil
	}
	v, ok := desired["parent"]
	if !ok || v.IsNull() || v.Type() != cty.String {
		return nil
	}
	s := v.AsString()
	if strings.HasPrefix(s, "mock_thing.") {
		return []provider.Ref{{Address: s, Attr: "id"}}
	}
	return nil
}

func clone(a provider.Attributes) provider.Attributes {
	out := make(provider.Attributes, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}
