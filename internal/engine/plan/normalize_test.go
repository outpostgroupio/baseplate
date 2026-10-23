package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// lowerName wraps a mock resource with a Normalizer that lowercases "name"
// (standing in for e.g. timestamp canonicalisation) and rejects "bad".
type lowerName struct{ provider.Resource }

func (l lowerName) Normalize(d provider.Attributes) (provider.Attributes, error) {
	out := provider.Attributes{}
	for k, v := range d {
		out[k] = v
	}
	if v, ok := d["name"]; ok && v.IsKnown() && !v.IsNull() {
		if v.AsString() == "bad" {
			return nil, errors.New(`name: "bad" is not allowed`)
		}
		out["name"] = cty.StringVal(strings.ToLower(v.AsString()))
	}
	return out, nil
}

type normProvider struct{ m *mock.Provider }

func (n normProvider) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{"mock_thing": lowerName{n.m.Resources()["mock_thing"]}}
}

func TestNormalizerSuppressesEquivalentDiff(t *testing.T) {
	m := mock.New()
	m.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("hello")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})
	desired := []DesiredResource{{Type: "mock_thing", Name: "a",
		Attributes: provider.Attributes{"name": cty.StringVal("HELLO")}}}

	pl, err := Build(context.Background(), normProvider{m}, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("normalized-equal value must not diff, got %+v", pl.Changes[0].AttrDiffs)
	}
	// Desired as rendered/applied stays as the user wrote it.
	if pl.Changes[0].Desired["name"].AsString() != "HELLO" {
		t.Fatalf("Desired must be the raw config value, got %v", pl.Changes[0].Desired["name"])
	}
}

func TestNormalizerErrorFailsPlanForCreateAndUpdate(t *testing.T) {
	desired := []DesiredResource{{Type: "mock_thing", Name: "a",
		Attributes: provider.Attributes{"name": cty.StringVal("bad")}}}

	// Create path (not in state).
	_, err := Build(context.Background(), normProvider{mock.New()}, state.New(), desired)
	if err == nil || !strings.Contains(err.Error(), "mock_thing.a") || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("create: want address-prefixed normalize error, got %v", err)
	}

	// Update path (in state).
	m := mock.New()
	m.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("x")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})
	if _, err := Build(context.Background(), normProvider{m}, s, desired); err == nil {
		t.Fatal("update: want normalize error")
	}
}

func TestNormalizerAppliedToImportDiff(t *testing.T) {
	m := mock.New()
	m.Seed("mock_thing/7", provider.Attributes{"name": cty.StringVal("hello")})
	desired := []DesiredResource{{Type: "mock_thing", Name: "a",
		Attributes: provider.Attributes{"name": cty.StringVal("Hello")}}}
	p := normProvider{m}
	s := state.New()
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveImports(context.Background(), p, s, pl, []ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/7"}}); err != nil {
		t.Fatal(err)
	}
	if pl.Changes[0].Action != provider.ActionNoop {
		t.Fatalf("import of a normalized-equal resource must be a no-op, got %s %+v", pl.Changes[0].Action, pl.Changes[0].AttrDiffs)
	}
}

func TestCtyStringRendersListsHCLStyle(t *testing.T) {
	cases := map[string]cty.Value{
		`["a", "b"]`: cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}),
		`[]`:         cty.ListValEmpty(cty.String),
		`[1, true]`:  cty.TupleVal([]cty.Value{cty.NumberIntVal(1), cty.True}),
	}
	for want, v := range cases {
		if got := ctyString(v); got != want {
			t.Errorf("ctyString = %s, want %s", got, want)
		}
	}
}
