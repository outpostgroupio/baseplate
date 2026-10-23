package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// forEachEnv drives config → plan → apply for for_each configs against one
// persistent mock provider and state, so successive steps see real results.
type forEachEnv struct {
	t   *testing.T
	ctx context.Context
	p   *mock.Provider
	s   *state.State
	b   backend.Backend
	dir string
}

func newForEachEnv(t *testing.T) *forEachEnv {
	dir := t.TempDir()
	return &forEachEnv{t: t, ctx: context.Background(), p: mock.New(), s: state.New(),
		b: backend.NewLocal(filepath.Join(dir, "s.bpstate")), dir: dir}
}

func (e *forEachEnv) plan(src string) *plan.Plan {
	e.t.Helper()
	path := filepath.Join(e.dir, "c.bp.hcl")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		e.t.Fatal(err)
	}
	_, desired, imports, err := config.Load(path, e.p)
	if err != nil {
		e.t.Fatal(err)
	}
	pl, err := plan.Build(e.ctx, e.p, e.s, desired)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := plan.ResolveImports(e.ctx, e.p, e.s, pl, imports); err != nil {
		e.t.Fatal(err)
	}
	return pl
}

func (e *forEachEnv) apply(pl *plan.Plan) {
	e.t.Helper()
	if err := Apply(e.ctx, e.p, e.b, e.s, pl); err != nil {
		e.t.Fatal(err)
	}
}

const threeThings = `
resource "mock_thing" "set" {
  for_each = toset(["a", "b", "c"])
  name     = "thing-${each.key}"
}
`

func TestForEachCreatesThenConverges(t *testing.T) {
	e := newForEachEnv(t)
	pl := e.plan(threeThings)
	if s := pl.Summary(); s.Add != 3 {
		t.Fatalf("want 3 creates, got %+v", s)
	}
	e.apply(pl)
	if len(e.p.Store()) != 3 || len(e.s.Resources) != 3 {
		t.Fatalf("want 3 remote objects and 3 state entries, got %d / %d", len(e.p.Store()), len(e.s.Resources))
	}
	r, ok := e.s.Find(`mock_thing.set["b"]`)
	if !ok || r.Key != "b" || r.Name != "set" {
		t.Fatalf("state entry for instance b = %+v", r)
	}
	if pl2 := e.plan(threeThings); pl2.HasChanges() {
		t.Fatalf("re-plan must converge:\n%s", pl2.RenderText(false))
	}
}

func TestForEachRemovingAKeyDeletesOnlyThatInstance(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.plan(threeThings))
	pl := e.plan(strings.Replace(threeThings, `"a", "b", "c"`, `"a", "c"`, 1))
	if len(pl.Changes) != 3 {
		t.Fatalf("want 3 changes (2 noop + 1 delete), got %+v", pl.Changes)
	}
	var deletes []string
	for _, c := range pl.Changes {
		if c.Action == provider.ActionDelete {
			deletes = append(deletes, c.Address)
		} else if c.Action != provider.ActionNoop {
			t.Fatalf("%s: unexpected %s", c.Address, c.Action)
		}
	}
	if len(deletes) != 1 || deletes[0] != `mock_thing.set["b"]` {
		t.Fatalf("deletes = %v, want only instance b", deletes)
	}
	e.apply(pl)
	if _, ok := e.s.Find(`mock_thing.set["b"]`); ok || len(e.s.Resources) != 2 {
		t.Fatalf("state after delete = %+v", e.s.Resources)
	}
}

func TestForEachInstanceRefResolvesAtApply(t *testing.T) {
	e := newForEachEnv(t)
	src := threeThings + `
resource "mock_ref" "r" {
  for_each = toset(["a", "c"])
  name     = "ref-${each.key}"
  target   = mock_thing.set[each.key].id
}
resource "mock_ref" "pinned" {
  name   = "pinned"
  target = mock_thing.set["b"].id
}
`
	e.apply(e.plan(src))
	idOf := func(a string) string {
		r, ok := e.s.Find(a)
		if !ok {
			t.Fatalf("%s not in state", a)
		}
		return r.ID
	}
	store := e.p.Store()
	for ref, target := range map[string]string{
		`mock_ref.r["a"]`: `mock_thing.set["a"]`,
		`mock_ref.r["c"]`: `mock_thing.set["c"]`,
		`mock_ref.pinned`: `mock_thing.set["b"]`,
	} {
		got := store[idOf(ref)]["target"]
		if got.IsNull() || got.AsString() != idOf(target) {
			t.Fatalf("%s.target = %#v, want %s's id %s", ref, got, target, idOf(target))
		}
	}
	if pl := e.plan(src); pl.HasChanges() {
		t.Fatalf("re-plan must converge:\n%s", pl.RenderText(false))
	}
}

func TestForEachImportAdoptsWithoutCreate(t *testing.T) {
	e := newForEachEnv(t)
	e.p.Seed("old/1", provider.Attributes{"name": cty.StringVal("thing-a")})
	e.p.Seed("old/2", provider.Attributes{"name": cty.StringVal("thing-b")})
	src := `
locals {
  existing = { a = "old/1", b = "old/2" }
}
resource "mock_thing" "set" {
  for_each = toset(["a", "b"])
  name     = "thing-${each.key}"
}
import {
  for_each = local.existing
  to       = mock_thing.set[each.key]
  id       = each.value
}
`
	pl := e.plan(src)
	if s := pl.Summary(); s.Import != 2 || s.Add != 0 {
		t.Fatalf("want 2 imports and no creates, got %+v\n%s", s, pl.RenderText(false))
	}
	e.apply(pl)
	if n := e.p.Creates(); n != 0 {
		t.Fatalf("adopting must not call Create, got %d call(s)", n)
	}
	if r, ok := e.s.Find(`mock_thing.set["b"]`); !ok || r.ID != "old/2" || r.Key != "b" {
		t.Fatalf("instance b not adopted with its id: %+v", r)
	}
	if pl2 := e.plan(src); pl2.HasChanges() {
		t.Fatalf("post-import plan must converge:\n%s", pl2.RenderText(false))
	}
}

func TestForEachKeyRenameWarns(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.plan(threeThings))
	pl := e.plan(strings.Replace(threeThings, `"a", "b", "c"`, `"a", "b", "z"`, 1))
	ws := pl.Warnings()
	if len(ws) != 1 || !strings.Contains(ws[0], "mock_thing.set: 1 instance(s) will be destroyed and 1 created") {
		t.Fatalf("expected a rename warning, got %v", ws)
	}
	if !strings.Contains(pl.RenderText(false), "Warning: mock_thing.set") {
		t.Fatalf("text plan must show the warning:\n%s", pl.RenderText(false))
	}
}

func TestSingularToForEachIsDeletePlusCreate(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.plan(`resource "mock_thing" "set" { name = "x" }`))
	pl := e.plan(`
resource "mock_thing" "set" {
  for_each = toset(["x"])
  name     = each.key
}
`)
	s := pl.Summary()
	if s.Add != 1 || s.Destroy != 1 {
		t.Fatalf("singular → for_each must plan create + delete, got %+v", s)
	}
	if len(pl.Warnings()) != 1 {
		t.Fatalf("the conversion should trigger the rename warning: %v", pl.Warnings())
	}
}

// A dependent that stays unchanged while one of its dependency instances is
// removed keeps the stale dependency in state (no-op changes aren't rewritten).
// Removing the dependent later must still plan, not fail on that stale edge.
func TestDeleteWithStaleRecordedDependencyPlans(t *testing.T) {
	e := newForEachEnv(t)
	child := `
resource "mock_child" "c" {
  name   = "child"
  parent = mock_thing.set
}
`
	e.apply(e.plan(threeThings + child))
	e.apply(e.plan(strings.Replace(threeThings, `"a", "b", "c"`, `"a", "c"`, 1) + child))
	pl := e.plan(strings.Replace(threeThings, `"a", "b", "c"`, `"a", "c"`, 1))
	if s := pl.Summary(); s.Destroy != 1 {
		t.Fatalf("want the child deleted, got %+v", s)
	}
	e.apply(pl)
}
