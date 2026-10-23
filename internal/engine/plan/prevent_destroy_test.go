package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
)

const preventDestroyMsg = "Instance cannot be destroyed: %s has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed. To avoid this error and continue with the plan, either disable lifecycle.prevent_destroy or reduce the scope of the plan using the --target flag."

func protectedThing(name, key string) DesiredResource {
	return DesiredResource{Type: "mock_thing", Name: name, Key: key, PreventDestroy: true,
		Attributes: provider.Attributes{"name": cty.StringVal(name + key)}}
}

// seeded returns a mock provider and a state holding the given mock_thing
// instances (name/key pairs), each with a live remote object.
func seeded(t *testing.T, insts ...[2]string) (*mock.Provider, *state.State) {
	t.Helper()
	p := mock.New()
	s := state.New()
	for i, nk := range insts {
		id := "mock_thing/" + string(rune('1'+i))
		p.Seed(id, provider.Attributes{"name": cty.StringVal(nk[0] + nk[1])})
		s.Upsert(state.Resource{Type: "mock_thing", Name: nk[0], Key: nk[1], ID: id})
	}
	return p, s
}

func wantPreventDestroy(t *testing.T, err error, addrs ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a prevent_destroy error for %v", addrs)
	}
	for _, a := range addrs {
		want := strings.Replace(preventDestroyMsg, "%s", a, 1)
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not contain\n  %s\ngot\n  %s", want, err)
		}
	}
}

// Destroy plans with no desired resources; the protected set comes from config
// (Plan.PreventDestroy), since destroy never builds desired instances.
func TestPreventDestroyBlocksDestroy(t *testing.T) {
	p, s := seeded(t, [2]string{"prod", ""}, [2]string{"scratch", ""})
	pl, err := Build(context.Background(), p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl.PreventDestroy = []string{"mock_thing.prod"}
	wantPreventDestroy(t, pl.CheckPreventDestroy(), "mock_thing.prod")
	if strings.Contains(pl.CheckPreventDestroy().Error(), "scratch") {
		t.Fatal("unprotected resources must not be reported")
	}
}

func TestPreventDestroyBlocksRemovedForEachKey(t *testing.T) {
	p, s := seeded(t, [2]string{"env", "prod"}, [2]string{"env", "staging"})
	// The block still exists (and still sets prevent_destroy), but "staging"
	// is gone from for_each.
	pl, err := Build(context.Background(), p, s, []DesiredResource{protectedThing("env", "prod")})
	if err != nil {
		t.Fatal(err)
	}
	wantPreventDestroy(t, pl.CheckPreventDestroy(), `mock_thing.env["staging"]`)
}

// A for_each that declares zero instances leaves no desired resource to carry
// the flag; the caller supplies the block from Config.PreventDestroy.
func TestPreventDestroyBlocksEmptyForEach(t *testing.T) {
	p, s := seeded(t, [2]string{"env", "prod"})
	pl, err := Build(context.Background(), p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl.PreventDestroy = append(pl.PreventDestroy, "mock_thing.env")
	wantPreventDestroy(t, pl.CheckPreventDestroy(), `mock_thing.env["prod"]`)
}

func TestPreventDestroyReportsEveryInstance(t *testing.T) {
	p, s := seeded(t, [2]string{"env", "a"}, [2]string{"env", "b"})
	pl, err := Build(context.Background(), p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl.PreventDestroy = []string{"mock_thing.env"}
	wantPreventDestroy(t, pl.CheckPreventDestroy(), `mock_thing.env["a"]`, `mock_thing.env["b"]`)
}

// Build derives the protected set from the desired resources it is given.
func TestBuildRecordsPreventDestroyFromDesired(t *testing.T) {
	pl, err := Build(context.Background(), mock.New(), state.New(), []DesiredResource{
		protectedThing("b", "y"), protectedThing("b", "x"), protectedThing("a", ""),
		{Type: "mock_thing", Name: "c", Attributes: provider.Attributes{"name": cty.StringVal("c")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pl.PreventDestroy, ","); got != "mock_thing.a,mock_thing.b" {
		t.Fatalf("PreventDestroy = %q", got)
	}
}

// Creates, updates and no-ops of a protected resource are all fine.
func TestPreventDestroyAllowsNonDeletes(t *testing.T) {
	p, s := seeded(t, [2]string{"prod", ""}, [2]string{"other", ""})
	d := protectedThing("prod", "")
	d.Attributes = provider.Attributes{"name": cty.StringVal("renamed")}
	pl, err := Build(context.Background(), p, s, []DesiredResource{
		d, protectedThing("other", ""), protectedThing("fresh", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !pl.HasChanges() {
		t.Fatal("expected an update and a create")
	}
	if err := pl.CheckPreventDestroy(); err != nil {
		t.Fatalf("no deletes, so no error; got %v", err)
	}
}

// Terraform only knows prevent_destroy from config: once the block (or its
// lifecycle setting) is removed, the resource is unprotected.
func TestPreventDestroyLiftedByRemovingLifecycle(t *testing.T) {
	p, s := seeded(t, [2]string{"env", "prod"}, [2]string{"env", "staging"})
	unprotected := protectedThing("env", "prod")
	unprotected.PreventDestroy = false
	pl, err := Build(context.Background(), p, s, []DesiredResource{unprotected})
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.CheckPreventDestroy(); err != nil {
		t.Fatalf("lifecycle removed, so the delete is allowed; got %v", err)
	}
	// Whole block removed: plain destroy-everything plan, nothing protected.
	pl, err = Build(context.Background(), p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.CheckPreventDestroy(); err != nil {
		t.Fatalf("block removed, so the delete is allowed; got %v", err)
	}
}

// The check looks only at the changes left in the plan, so narrowing the plan
// (what --target does) past a protected delete lifts the error.
func TestPreventDestroyIgnoresChangesNarrowedAway(t *testing.T) {
	p, s := seeded(t, [2]string{"prod", ""}, [2]string{"scratch", ""})
	pl, err := Build(context.Background(), p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl.PreventDestroy = []string{"mock_thing.prod"}
	var kept []Change
	for _, c := range pl.Changes {
		if c.Name == "scratch" {
			kept = append(kept, c)
		}
	}
	pl.Changes = kept
	if err := pl.CheckPreventDestroy(); err != nil {
		t.Fatalf("protected resource is outside the plan; got %v", err)
	}
}
