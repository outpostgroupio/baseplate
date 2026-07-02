package plan

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/mock"
	"github.com/outpostgroup/baseplate/internal/state"
)

func TestBuildCreateAndOrder(t *testing.T) {
	p := mock.New()
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_child", Name: "c", Attributes: provider.Attributes{
			"name": cty.StringVal("child"), "parent": cty.StringVal("mock_thing.a")},
			DependsOn: []string{"mock_thing.a"}},
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !pl.HasChanges() {
		t.Fatal("expected changes")
	}
	if pl.Changes[0].Address != "mock_thing.a" {
		t.Fatalf("expected parent first, got %s", pl.Changes[0].Address)
	}
	for _, c := range pl.Changes {
		if c.Action != provider.ActionCreate {
			t.Fatalf("%s: expected create, got %s", c.Address, c.Action)
		}
	}
}

// TestBuildDeleteReverseDependencyOrder guards that deletes run in reverse
// dependency order using the recorded state dependencies — NOT lexical order.
// Here the dependent (mock_child.a) sorts lexically BEFORE its dependency
// (mock_thing.z), so a lexical-only ordering would delete the dependency first
// (wrong). Correct order deletes the dependent first.
func TestBuildDeleteReverseDependencyOrder(t *testing.T) {
	p := mock.New()
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "z", ID: "mock_thing/1"})
	s.Upsert(state.Resource{Type: "mock_child", Name: "a", ID: "mock_child/1",
		Dependencies: []string{"mock_thing.z"}})

	// Empty desired → everything is a delete.
	pl, err := Build(context.Background(), p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 2 {
		t.Fatalf("expected 2 deletes, got %d", len(pl.Changes))
	}
	for _, c := range pl.Changes {
		if c.Action != provider.ActionDelete {
			t.Fatalf("%s: expected delete, got %s", c.Address, c.Action)
		}
	}
	if pl.Changes[0].Address != "mock_child.a" || pl.Changes[1].Address != "mock_thing.z" {
		t.Fatalf("dependent must be deleted before its dependency; got order %s, %s",
			pl.Changes[0].Address, pl.Changes[1].Address)
	}
}
