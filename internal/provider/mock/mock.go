package mock

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

type Provider struct {
	mu      sync.Mutex
	store   map[string]provider.Attributes
	seq     int
	creates int
	deletes int
	// fail makes the next call of an operation ("create", "delete") fail with
	// the given error (consumed once), for failure-path tests.
	fail map[string]error
}

// FailNext makes the next call of op ("create" or "delete") return err.
func (p *Provider) FailNext(op string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail == nil {
		p.fail = map[string]error{}
	}
	p.fail[op] = err
}

func (p *Provider) takeFail(op string) error {
	err := p.fail[op]
	delete(p.fail, op)
	return err
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

// Creates returns how many Create calls the provider has served, so tests can
// assert an operation adopted resources rather than creating them.
func (p *Provider) Creates() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.creates
}

// Deletes returns how many Delete calls the provider has served.
func (p *Provider) Deletes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deletes
}

func (p *Provider) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{
		"mock_thing":    &res{p: p, typ: "mock_thing"},
		"mock_child":    &res{p: p, typ: "mock_child"},
		"mock_computed": &res{p: p, typ: "mock_computed"},
		"mock_ref":      &res{p: p, typ: "mock_ref"},
		"mock_trigger":  &res{p: p, typ: "mock_trigger"},
		"mock_secret":   &res{p: p, typ: "mock_secret"},
		"mock_replace":  &replaceRes{res{p: p, typ: "mock_replace"}},
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
	if r.typ == "mock_ref" {
		// Required on purpose: exercises that a REQUIRED attribute which is a
		// reference (unknown until apply) still decodes cleanly.
		spec["target"] = &hcldec.AttrSpec{Name: "target", Type: cty.String, Required: true}
	}
	if r.typ == "mock_trigger" || r.typ == "mock_replace" {
		spec["rev"] = &hcldec.AttrSpec{Name: "rev", Type: cty.String, Required: false}
	}
	if r.typ == "mock_replace" {
		spec["kind"] = &hcldec.AttrSpec{Name: "kind", Type: cty.String, Required: false}
	}
	if r.typ == "mock_secret" {
		spec["value"] = &hcldec.AttrSpec{Name: "value", Type: cty.String, Required: false}
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
	if err := r.p.takeFail("create"); err != nil {
		return "", nil, err
	}
	r.p.seq++
	r.p.creates++
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
	r.p.deletes++
	if err := r.p.takeFail("delete"); err != nil {
		return err
	}
	delete(r.p.store, id)
	return nil
}

func (r *res) Computed() []string {
	if r.typ == "mock_computed" {
		return []string{"server_id"}
	}
	if r.typ == "mock_trigger" || r.typ == "mock_replace" {
		return []string{"rev"}
	}
	return nil
}

// Sensitive marks mock_secret's value as sensitive (redacted from output).
func (r *res) Sensitive() []string {
	if r.typ == "mock_secret" {
		return []string{"value"}
	}
	return nil
}

func (r *res) Triggers(desired provider.Attributes) (map[string]cty.Value, error) {
	if r.typ != "mock_trigger" && r.typ != "mock_replace" {
		return nil, nil
	}
	out := map[string]cty.Value{}
	if v, ok := desired["rev"]; ok && !v.IsNull() {
		out["rev"] = v
	}
	return out, nil
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

// replaceRes is mock_replace: a change to "kind" (an attribute) or "rev" (a
// trigger) forces a replace, unless kind is "inplace", in which case only
// "kind" does (mirroring roblox_asset, where a Model takes new content in
// place).
type replaceRes struct{ res }

func (r *replaceRes) RequiresReplace(desired provider.Attributes) []string {
	if v, ok := desired["kind"]; ok && v.IsKnown() && !v.IsNull() && v.AsString() == "inplace" {
		return []string{"kind"}
	}
	return []string{"kind", "rev"}
}

func clone(a provider.Attributes) provider.Attributes {
	out := make(provider.Attributes, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}
