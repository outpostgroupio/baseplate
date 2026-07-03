package apply

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/mock"
	"github.com/outpostgroup/baseplate/internal/state"
	"github.com/outpostgroup/baseplate/internal/state/backend"
)

func TestApplyCreatesAndPersists(t *testing.T) {
	p := mock.New()
	s := state.New()
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
	}
	pl, err := plan.Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))
	if err := Apply(context.Background(), p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	if len(p.Store()) != 1 {
		t.Fatalf("expected 1 remote object, got %d", len(p.Store()))
	}
	persisted, _ := b.Get()
	if _, ok := persisted.Find("mock_thing.a"); !ok {
		t.Fatal("state not persisted after apply")
	}
}

// TestApplyThenReplanConverges is the end-to-end Phase 0 proof: after applying,
// a fresh plan against the SAME (persistent) provider + resulting state must
// report no changes — idempotent reconciliation with no spurious diffs. It also
// exercises a cross-resource dependency (mock_child.c → mock_thing.a).
func TestApplyThenReplanConverges(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
		{Type: "mock_child", Name: "c", DependsOn: []string{"mock_thing.a"}, Attributes: provider.Attributes{
			"name": cty.StringVal("child"), "parent": cty.StringVal("mock_thing.a")}},
	}

	first, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasChanges() {
		t.Fatal("first plan should have changes")
	}

	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))
	if err := Apply(ctx, p, b, s, first); err != nil {
		t.Fatal(err)
	}

	// Re-plan against the same provider (store persisted) and the post-apply state.
	second, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasChanges() {
		t.Fatalf("re-plan after apply must converge to no changes, got: %s", second.RenderText(false))
	}
}

func TestApplyThenDestroyDeletesDependentFirst(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	// child "a" depends on parent "z"; note "mock_child.a" < "mock_thing.z" lexically.
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "z", Attributes: provider.Attributes{"name": cty.StringVal("parent")}},
		{Type: "mock_child", Name: "a", DependsOn: []string{"mock_thing.z"}, Attributes: provider.Attributes{
			"name": cty.StringVal("child"), "parent": cty.StringVal("mock_thing.z")}},
	}
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}

	// Destroy: empty desired → both become deletes, ordered from persisted state deps.
	dpl, err := plan.Build(ctx, p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dpl.Changes) != 2 {
		t.Fatalf("expected 2 deletes, got %d", len(dpl.Changes))
	}
	if dpl.Changes[0].Address != "mock_child.a" || dpl.Changes[1].Address != "mock_thing.z" {
		t.Fatalf("dependent must delete before dependency; got %s then %s",
			dpl.Changes[0].Address, dpl.Changes[1].Address)
	}
	if err := Apply(ctx, p, b, s, dpl); err != nil {
		t.Fatal(err)
	}
	if len(p.Store()) != 0 {
		t.Fatalf("expected empty store after destroy, got %d", len(p.Store()))
	}
}

func TestApplyResolvesReferenceFromDependencyOutput(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
		{Type: "mock_ref", Name: "b", DependsOn: []string{"mock_thing.a"},
			Attributes: provider.Attributes{"name": cty.StringVal("child")},
			Refs:       map[string]provider.Ref{"target": {Address: "mock_thing.a", Attr: "id"}}},
	}
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}

	// The mock_thing's real id (its state ID) must have been injected into
	// mock_ref.b's "target" and stored by the provider.
	var thingID, refTarget string
	for id, a := range p.Store() {
		if a["name"].AsString() == "root" {
			thingID = id
		}
		if v, ok := a["target"]; ok {
			refTarget = v.AsString()
		}
	}
	if thingID == "" {
		t.Fatal("mock_thing not created")
	}
	if refTarget != thingID {
		t.Fatalf("reference not resolved: target=%q want thing id %q", refTarget, thingID)
	}
}

func TestApplyThenReplanConvergesWithReference(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
		{Type: "mock_ref", Name: "b", DependsOn: []string{"mock_thing.a"},
			Attributes: provider.Attributes{"name": cty.StringVal("child")},
			Refs:       map[string]provider.Ref{"target": {Address: "mock_thing.a", Attr: "id"}}},
	}
	first, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, first); err != nil {
		t.Fatal(err)
	}
	// Re-plan: the resolved reference (target = real id) must diff against the
	// resolved value from state, not null, so there are no changes.
	second, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasChanges() {
		t.Fatalf("re-plan with a resolved reference must converge, got: %s", second.RenderText(false))
	}
}

// TestApplyThenReplanConvergesWithCollectionReference guards the plan/apply
// reference-resolution asymmetry: apply stores a resolved reference as a live
// cty.Value, while plan reconstructs it from state via ctyjson against
// DynamicPseudoType (a JSON array reloads as a TUPLE, not a list). Without a
// shared canonical form the referenced collection diffs against itself forever.
func TestApplyThenReplanConvergesWithCollectionReference(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	tags := cty.ListVal([]cty.Value{cty.StringVal("x"), cty.StringVal("y")})
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{
			"name": cty.StringVal("root"), "tags": tags}},
		{Type: "mock_ref", Name: "b", DependsOn: []string{"mock_thing.a"},
			Attributes: provider.Attributes{"name": cty.StringVal("child")},
			Refs:       map[string]provider.Ref{"target": {Address: "mock_thing.a", Attr: "tags"}}},
	}

	first, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, first); err != nil {
		t.Fatal(err)
	}

	second, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasChanges() {
		t.Fatalf("re-plan with a collection reference must converge, got: %s", second.RenderText(false))
	}
}

func TestApplyDeleteRemovesResourceAndState(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
	}
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}

	// Now plan with empty desired → delete, and apply it.
	dpl, err := plan.Build(ctx, p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, dpl); err != nil {
		t.Fatal(err)
	}
	if len(p.Store()) != 0 {
		t.Fatalf("expected remote object deleted, store has %d", len(p.Store()))
	}
	persisted, _ := b.Get()
	if _, ok := persisted.Find("mock_thing.a"); ok {
		t.Fatal("state should no longer contain the deleted resource")
	}
}
