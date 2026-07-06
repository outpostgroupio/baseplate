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

func TestBuildUpdateWhenAttrChanged(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("old")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})

	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("new")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 1 || pl.Changes[0].Action != provider.ActionUpdate {
		t.Fatalf("expected 1 update, got %+v", pl.Changes)
	}
	if len(pl.Changes[0].AttrDiffs) != 1 || pl.Changes[0].AttrDiffs[0].Name != "name" {
		t.Fatalf("expected a name diff, got %+v", pl.Changes[0].AttrDiffs)
	}
}

func TestBuildDriftRecreatesWhenRemoteMissing(t *testing.T) {
	p := mock.New() // provider store is EMPTY → Read returns ErrNotFound
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})

	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 1 || pl.Changes[0].Action != provider.ActionCreate {
		t.Fatalf("expected drift → recreate (ActionCreate), got %+v", pl.Changes)
	}
}

func TestUndeclaredActualAttrNotADiff(t *testing.T) {
	p := mock.New()
	// Remote has an extra attribute the config never declares.
	p.Seed("mock_thing/1", provider.Attributes{
		"name": cty.StringVal("x"), "extra": cty.StringVal("from-server")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})

	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("an attribute the config does not declare must not diff, got: %+v", pl.Changes)
	}
}

func TestComputedAttrNotADiff(t *testing.T) {
	p := mock.New()
	// Remote has a computed server_id the user never authored.
	p.Seed("mock_computed/1", provider.Attributes{
		"name": cty.StringVal("x"), "server_id": cty.StringVal("srv-1")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_computed", Name: "a", ID: "mock_computed/1"})

	desired := []DesiredResource{
		{Type: "mock_computed", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("computed-only difference must not be a change, got: %+v", pl.Changes)
	}
}

func TestResolveImportsPendingImportConverges(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("mock_thing/7", provider.Attributes{"name": cty.StringVal("adopted")})
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("adopted")}},
	}
	pl, err := Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	imports := []ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/7"}}
	if err := ResolveImports(ctx, p, s, pl, imports); err != nil {
		t.Fatal(err)
	}
	if !pl.HasChanges() {
		t.Fatal("a pending import is a change")
	}
	c := pl.Changes[0]
	if c.ImportID != "mock_thing/7" {
		t.Fatalf("expected ImportID set, got %q", c.ImportID)
	}
	if c.Action != provider.ActionNoop {
		t.Fatalf("config matches live → post-import reconcile should be Noop, got %v", c.Action)
	}
}

func TestResolveImportsShowsUpdateWhenConfigDiffers(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("mock_thing/7", provider.Attributes{"name": cty.StringVal("old")})
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("new")}},
	}
	pl, _ := Build(ctx, p, s, desired)
	if err := ResolveImports(ctx, p, s, pl, []ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/7"}}); err != nil {
		t.Fatal(err)
	}
	if pl.Changes[0].Action != provider.ActionUpdate {
		t.Fatalf("config differs from live → expected Update, got %v", pl.Changes[0].Action)
	}
}

func TestResolveImportsErrors(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	}

	// not declared
	pl, _ := Build(ctx, p, s, desired)
	if err := ResolveImports(ctx, p, s, pl, []ImportBlock{{Address: "mock_thing.ghost", ID: "id"}}); err == nil {
		t.Fatal("want a not-declared error")
	}
	// not found (empty store)
	pl2, _ := Build(ctx, p, s, desired)
	if err := ResolveImports(ctx, p, s, pl2, []ImportBlock{{Address: "mock_thing.a", ID: "missing"}}); err == nil {
		t.Fatal("want a not-found error")
	}
	// duplicate
	p.Seed("id1", provider.Attributes{"name": cty.StringVal("x")})
	pl3, _ := Build(ctx, p, s, desired)
	dup := []ImportBlock{{Address: "mock_thing.a", ID: "id1"}, {Address: "mock_thing.a", ID: "id1"}}
	if err := ResolveImports(ctx, p, s, pl3, dup); err == nil {
		t.Fatal("want a duplicate-import error")
	}
}
