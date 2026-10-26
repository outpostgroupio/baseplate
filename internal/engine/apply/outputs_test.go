package apply

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// outProvider has out_thing, whose computed output "tex" is reported only
// while report is set (a best-effort lookup that can fail), and out_ref, which
// records the "target" it was created/updated with (a reference to out_thing).
type outProvider struct {
	report bool
	next   int
	store  map[string]provider.Attributes
	got    map[string]cty.Value // out_ref address-less: last "target" received
}

func newOutProvider() *outProvider {
	return &outProvider{store: map[string]provider.Attributes{}, got: map[string]cty.Value{}}
}

func (p *outProvider) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{
		"out_thing": &outThing{p: p},
		"out_ref":   &outRef{p: p},
	}
}

type outThing struct{ p *outProvider }

var _ provider.Outputter = (*outThing)(nil)

func (r *outThing) Type() string { return "out_thing" }
func (r *outThing) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{"name": &hcldec.AttrSpec{Name: "name", Type: cty.String, Required: true}}
}
func (r *outThing) Outputs() map[string]cty.Type                    { return map[string]cty.Type{"tex": cty.String} }
func (r *outThing) Computed() []string                              { return []string{"id", "tex"} }
func (r *outThing) Dependencies(provider.Attributes) []provider.Ref { return nil }
func (r *outThing) attrs(id string, name cty.Value) provider.Attributes {
	out := provider.Attributes{"name": name}
	if r.p.report {
		out["tex"] = cty.StringVal("tex-" + id)
	}
	return out
}
func (r *outThing) Read(_ context.Context, id string) (provider.Attributes, error) {
	a, ok := r.p.store[id]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return r.attrs(id, a["name"]), nil
}
func (r *outThing) Create(_ context.Context, d provider.Attributes) (string, provider.Attributes, error) {
	r.p.next++
	id := fmt.Sprint(r.p.next)
	r.p.store[id] = provider.Attributes{"name": d["name"]}
	return id, r.attrs(id, d["name"]), nil
}
func (r *outThing) Update(_ context.Context, id string, d provider.Attributes) (provider.Attributes, error) {
	r.p.store[id] = provider.Attributes{"name": d["name"]}
	return r.attrs(id, d["name"]), nil
}
func (r *outThing) Delete(_ context.Context, id string) error { delete(r.p.store, id); return nil }

type outRef struct{ p *outProvider }

func (r *outRef) Type() string { return "out_ref" }
func (r *outRef) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"name":   &hcldec.AttrSpec{Name: "name", Type: cty.String, Required: true},
		"target": &hcldec.AttrSpec{Name: "target", Type: cty.String, Required: true},
	}
}
func (r *outRef) Computed() []string                              { return []string{"id"} }
func (r *outRef) Dependencies(provider.Attributes) []provider.Ref { return nil }
func (r *outRef) Read(_ context.Context, id string) (provider.Attributes, error) {
	a, ok := r.p.store[id]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return a, nil
}
func (r *outRef) Create(_ context.Context, d provider.Attributes) (string, provider.Attributes, error) {
	r.p.next++
	id := fmt.Sprint(r.p.next)
	r.p.store[id] = provider.Attributes{"name": d["name"], "target": d["target"]}
	r.p.got["target"] = d["target"]
	return id, r.p.store[id], nil
}
func (r *outRef) Update(_ context.Context, id string, d provider.Attributes) (provider.Attributes, error) {
	r.p.store[id] = provider.Attributes{"name": d["name"], "target": d["target"]}
	r.p.got["target"] = d["target"]
	return r.p.store[id], nil
}
func (r *outRef) Delete(_ context.Context, id string) error { delete(r.p.store, id); return nil }

type outFixture struct {
	p *outProvider
	s *state.State
	b backend.Backend
}

func newOutFixture(t *testing.T) *outFixture {
	return &outFixture{p: newOutProvider(), s: state.New(), b: backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))}
}

func (f *outFixture) apply(t *testing.T, desired []plan.DesiredResource) error {
	t.Helper()
	pl, err := plan.Build(context.Background(), f.p, f.s, desired)
	if err != nil {
		t.Fatal(err)
	}
	return Apply(context.Background(), f.p, f.b, f.s, pl)
}

func (f *outFixture) tex(t *testing.T, address string) (string, bool) {
	t.Helper()
	r, ok := f.s.Find(address)
	if !ok {
		t.Fatalf("%s not in state", address)
	}
	raw, ok := r.Attributes["tex"]
	if !ok {
		return "", false
	}
	v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType)
	if err != nil {
		t.Fatal(err)
	}
	return v.AsString(), true
}

func thingDesired(name string) []plan.DesiredResource {
	return []plan.DesiredResource{{Type: "out_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal(name)}}}
}

func withRef(d []plan.DesiredResource, name string) []plan.DesiredResource {
	return append(d, plan.DesiredResource{
		Type: "out_ref", Name: "b", DependsOn: []string{"out_thing.a"},
		Attributes: provider.Attributes{"name": cty.StringVal(name)},
		Refs:       map[string]provider.Ref{"target": {Address: "out_thing.a", Attr: "tex"}},
	})
}

// State written before an output existed (or while its lookup failed) gets
// the output on the next apply, even though nothing else changed; a further
// converged apply doesn't rewrite state.
func TestApplyNoopRecordsNewOutput(t *testing.T) {
	f := newOutFixture(t)
	if err := f.apply(t, thingDesired("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.tex(t, "out_thing.a"); ok {
		t.Fatal("tex must be absent while the provider doesn't report it")
	}
	f.p.report = true
	pl, err := plan.Build(context.Background(), f.p, f.s, thingDesired("x"))
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("a new output must not be a change:\n%s", pl.RenderText(false))
	}
	if err := Apply(context.Background(), f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	if got, ok := f.tex(t, "out_thing.a"); !ok || got != "tex-1" {
		t.Fatalf("tex = %q (%v), want tex-1 recorded by the no-op apply", got, ok)
	}
	persisted, _ := f.b.Get()
	if r, _ := persisted.Find("out_thing.a"); r.Attributes["tex"] == nil {
		t.Fatal("the recorded output must be persisted")
	}
	serial := f.s.Serial
	if err := f.apply(t, thingDesired("x")); err != nil {
		t.Fatal(err)
	}
	if f.s.Serial != serial {
		t.Fatalf("a converged apply must not rewrite state (serial %d -> %d)", serial, f.s.Serial)
	}
}

// A recorded output survives a Read or an Update that doesn't report it, and
// a reference to it still resolves.
func TestApplyKeepsRecordedOutput(t *testing.T) {
	f := newOutFixture(t)
	f.p.report = true
	if err := f.apply(t, thingDesired("x")); err != nil {
		t.Fatal(err)
	}
	f.p.report = false
	// No-op on out_thing (Read lacks tex); out_ref is created referencing it.
	if err := f.apply(t, withRef(thingDesired("x"), "b1")); err != nil {
		t.Fatal(err)
	}
	if got, ok := f.tex(t, "out_thing.a"); !ok || got != "tex-1" {
		t.Fatalf("tex = %q (%v), want the recorded tex-1 kept", got, ok)
	}
	if got := f.p.got["target"]; got.IsNull() || got.AsString() != "tex-1" {
		t.Fatalf("reference resolved to %#v, want tex-1", got)
	}
	// Update on out_thing (rename) whose result lacks tex.
	if err := f.apply(t, withRef(thingDesired("y"), "b1")); err != nil {
		t.Fatal(err)
	}
	if got, ok := f.tex(t, "out_thing.a"); !ok || got != "tex-1" {
		t.Fatalf("after update tex = %q (%v), want tex-1 kept", got, ok)
	}
}

// A reference to an output the provider couldn't determine fails with an
// error that says so, rather than a generic "not found".
func TestApplyMissingOutputReferenceError(t *testing.T) {
	f := newOutFixture(t)
	err := f.apply(t, withRef(thingDesired("x"), "b1"))
	if err == nil {
		t.Fatal("want an error referencing an unset output")
	}
	if !strings.Contains(err.Error(), "out_thing.a.tex") || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("unexpected error: %v", err)
	}
}
