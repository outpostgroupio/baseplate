package apply

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// Two state entries sharing one remote id set up a destroy of an object that
// is still managed under another address (for a resource with a real delete,
// like roblox_universe_secret, that deletes the adopted object). Every import
// path must refuse an id that another entry of the same type already holds.

func guardFixture(t *testing.T) (*mock.Provider, *state.State, []plan.DesiredResource) {
	t.Helper()
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("a")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "a", ID: "mock_thing/1"})
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "set", Key: "a", Attributes: provider.Attributes{"name": cty.StringVal("a")}},
		{Type: "mock_thing", Name: "set", Key: "b", Attributes: provider.Attributes{"name": cty.StringVal("a")}},
	}
	return p, s, desired
}

func TestImportRefusesIDHeldByAnotherAddress(t *testing.T) {
	p, s, desired := guardFixture(t)
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))
	err := Import(context.Background(), p, b, s, `mock_thing.set["b"]`, "mock_thing/1", desired)
	if err == nil || !strings.Contains(err.Error(), `already managed as mock_thing.set["a"]`) {
		t.Fatalf("expected an id-collision error naming the existing address, got %v", err)
	}
	if _, ok := s.Find(`mock_thing.set["b"]`); ok {
		t.Fatal("the colliding import must not be written to state")
	}
}

func TestResolveImportsRefusesIDHeldByAnotherAddress(t *testing.T) {
	p, s, desired := guardFixture(t)
	pl, err := plan.Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	err = plan.ResolveImports(context.Background(), p, s, pl,
		[]plan.ImportBlock{{Address: `mock_thing.set["b"]`, ID: "mock_thing/1"}})
	if err == nil || !strings.Contains(err.Error(), `already managed as mock_thing.set["a"]`) {
		t.Fatalf("expected an id-collision error naming the existing address, got %v", err)
	}
}

func TestResolveImportsRefusesSameIDForTwoTargets(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("a")})
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "set", Key: "a", Attributes: provider.Attributes{"name": cty.StringVal("a")}},
		{Type: "mock_thing", Name: "set", Key: "b", Attributes: provider.Attributes{"name": cty.StringVal("a")}},
	}
	s := state.New()
	pl, err := plan.Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	err = plan.ResolveImports(context.Background(), p, s, pl, []plan.ImportBlock{
		{Address: `mock_thing.set["a"]`, ID: "mock_thing/1"},
		{Address: `mock_thing.set["b"]`, ID: "mock_thing/1"},
	})
	if err == nil || !strings.Contains(err.Error(), `mock_thing.set["a"]`) || !strings.Contains(err.Error(), "mock_thing/1") {
		t.Fatalf("expected a duplicate-id error, got %v", err)
	}
}

// Defence in depth: Apply itself refuses an import change whose id another
// state entry already holds (e.g. state changed between plan and apply).
func TestApplyRefusesImportIDHeldByAnotherAddress(t *testing.T) {
	p, s, _ := guardFixture(t)
	pl := &plan.Plan{Changes: []plan.Change{{
		Address: `mock_thing.set["b"]`, Type: "mock_thing", Name: "set", Key: "b",
		ID: "mock_thing/1", ImportID: "mock_thing/1", Action: provider.ActionNoop,
	}}}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))
	err := Apply(context.Background(), p, b, s, pl)
	if err == nil || !strings.Contains(err.Error(), `already managed as mock_thing.set["a"]`) {
		t.Fatalf("expected Apply to refuse the colliding import, got %v", err)
	}
	if len(s.Resources) != 1 {
		t.Fatalf("state must be untouched, got %+v", s.Resources)
	}
}

// An import block left in config after it was satisfied (same address, same
// id) is not a collision.
func TestImportGuardAllowsSameAddress(t *testing.T) {
	p, s, desired := guardFixture(t)
	pl, err := plan.Build(context.Background(), p, s, desired[:1])
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ResolveImports(context.Background(), p, s, pl,
		[]plan.ImportBlock{{Address: `mock_thing.set["a"]`, ID: "mock_thing/1"}}); err != nil {
		t.Fatalf("a satisfied import must stay a no-op, got %v", err)
	}
}
