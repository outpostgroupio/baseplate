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
