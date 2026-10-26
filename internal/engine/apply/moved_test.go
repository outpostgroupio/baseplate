package apply

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

// planMoved is forEachEnv.plan with moved blocks: load, apply the moves to the
// in-memory state, build, resolve imports and run the prevent_destroy check,
// as the CLI does.
func (e *forEachEnv) planMoved(src string) (*plan.Plan, error) {
	e.t.Helper()
	path := filepath.Join(e.dir, "c.bp.hcl")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		e.t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path, e.p)
	if err != nil {
		return nil, err
	}
	moves, err := plan.ApplyMoves(e.s, cfg.Moved)
	if err != nil {
		return nil, err
	}
	pl, err := plan.Build(e.ctx, e.p, e.s, cfg.Resources)
	if err != nil {
		return nil, err
	}
	pl.Moves = moves
	if err := plan.ResolveImports(e.ctx, e.p, e.s, pl, cfg.Imports); err != nil {
		return nil, err
	}
	pl.PreventDestroy = append(pl.PreventDestroy, cfg.PreventDestroy...)
	return pl, pl.CheckPreventDestroy()
}

func (e *forEachEnv) mustPlanMoved(src string) *plan.Plan {
	e.t.Helper()
	pl, err := e.planMoved(src)
	if err != nil {
		e.t.Fatal(err)
	}
	return pl
}

// ids returns address=id for every state entry, sorted.
func (e *forEachEnv) ids() []string {
	var out []string
	for _, r := range e.s.Resources {
		out = append(out, r.Address()+"="+r.ID)
	}
	sort.Strings(out)
	return out
}

const spritesOld = `
resource "mock_thing" "sprites" {
  for_each = toset(["ui/coin.png", "ui/gem.png", "units/ship.png"])
  name     = "sprite"
}
`

// The issue #2 case: a directory of files moves (ui/ -> icons/). One moved
// block with for_each relocates every instance: no Create, no Delete, same ids.
func TestMovedForEachDirectoryMoveKeepsIDs(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.mustPlanMoved(spritesOld))
	before := e.ids()
	creates := e.p.Creates()

	moved := `
locals {
  renames = {
    "ui/coin.png" = "icons/coin.png"
    "ui/gem.png"  = "icons/gem.png"
  }
}
resource "mock_thing" "sprites" {
  for_each = toset(["icons/coin.png", "icons/gem.png", "units/ship.png"])
  name     = "sprite"
}
moved {
  for_each = local.renames
  from     = mock_thing.sprites[each.key]
  to       = mock_thing.sprites[each.value]
}
`
	pl := e.mustPlanMoved(moved)
	if len(pl.Moves) != 2 {
		t.Fatalf("want 2 moves, got %v", pl.Moves)
	}
	for _, c := range pl.Changes {
		if c.Action != provider.ActionNoop {
			t.Fatalf("a moved instance must plan as a no-op, got %s %s", c.Action, c.Address)
		}
	}
	if !pl.HasChanges() || pl.Warnings() != nil {
		t.Fatalf("moves are changes, and no rename warning: changes=%v warnings=%v", pl.HasChanges(), pl.Warnings())
	}
	e.apply(pl)
	if e.p.Creates() != creates || e.p.Deletes() != 0 {
		t.Fatalf("a move must not create or delete: creates %d -> %d, deletes %d", creates, e.p.Creates(), e.p.Deletes())
	}
	after := e.ids()
	idOf := func(list []string, a string) string {
		for _, x := range list {
			if strings.HasPrefix(x, a+"=") {
				return strings.TrimPrefix(x, a+"=")
			}
		}
		return ""
	}
	for old, nu := range map[string]string{"ui/coin.png": "icons/coin.png", "ui/gem.png": "icons/gem.png"} {
		o, n := idOf(before, `mock_thing.sprites["`+old+`"]`), idOf(after, `mock_thing.sprites["`+nu+`"]`)
		if o == "" || o != n {
			t.Fatalf("%s -> %s: id %q became %q", old, nu, o, n)
		}
	}
	// Persisted, not just in memory.
	persisted, err := e.b.Get()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := persisted.Find(`mock_thing.sprites["icons/gem.png"]`); !ok {
		t.Fatal("moved address not persisted")
	}
	if _, ok := persisted.Find(`mock_thing.sprites["ui/gem.png"]`); ok {
		t.Fatal("old address still persisted")
	}
	// Converged, and the moved block is now a no-op (idempotent).
	if pl2 := e.mustPlanMoved(moved); pl2.HasChanges() {
		t.Fatalf("re-plan must converge:\n%s", pl2.RenderText(false))
	}
}

// A plan whose only change is a move still persists the move on apply.
func TestMovedOnlyApplyPersists(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.mustPlanMoved(`
resource "mock_thing" "old" {
  name = "x"
}`))
	serial := e.s.Serial
	pl := e.mustPlanMoved(`
resource "mock_thing" "new" {
  name = "x"
}
moved {
  from = mock_thing.old
  to   = mock_thing.new
}`)
	if len(pl.Moves) != 1 || len(pl.Changes) != 1 || pl.Changes[0].Action != provider.ActionNoop {
		t.Fatalf("want one move and one no-op, got moves=%v changes=%+v", pl.Moves, pl.Changes)
	}
	e.apply(pl)
	persisted, err := e.b.Get()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := persisted.Find("mock_thing.new"); !ok || persisted.Serial <= serial {
		t.Fatalf("move not persisted (serial %d -> %d): %+v", serial, persisted.Serial, persisted.Resources)
	}
}

// singular -> for_each adoption: the existing object becomes one instance.
func TestMovedSingularAdoptsForEach(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.mustPlanMoved(`
resource "mock_thing" "icon" {
  name = "gem"
}`))
	pl := e.mustPlanMoved(`
resource "mock_thing" "icon" {
  for_each = toset(["gem", "coin"])
  name     = each.key
}
moved {
  from = mock_thing.icon
  to   = mock_thing.icon["gem"]
}`)
	if s := pl.Summary(); s.Move != 1 || s.Add != 1 || s.Destroy != 0 || s.Change != 0 {
		t.Fatalf("want 1 move + 1 add (coin), got %+v\n%s", s, pl.RenderText(false))
	}
	e.apply(pl)
	if e.p.Deletes() != 0 || e.p.Creates() != 2 {
		t.Fatalf("creates=%d deletes=%d", e.p.Creates(), e.p.Deletes())
	}
}

// Moving into an address the config doesn't declare follows Terraform: the
// move happens, then plan destroys the object at its new address.
func TestMovedToUndeclaredTargetPlansDelete(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.mustPlanMoved(`
resource "mock_thing" "a" {
  name = "x"
}`))
	pl := e.mustPlanMoved(`
moved {
  from = mock_thing.a
  to   = mock_thing.b
}`)
	if len(pl.Moves) != 1 || len(pl.Changes) != 1 || pl.Changes[0].Action != provider.ActionDelete || pl.Changes[0].Address != "mock_thing.b" {
		t.Fatalf("want a move then a delete of mock_thing.b, got moves=%v changes=%+v", pl.Moves, pl.Changes)
	}
}

// prevent_destroy is checked at the new address: a moved, still-declared
// instance is fine; one moved to an undeclared key is a protected delete.
func TestMovedPreventDestroyUsesNewAddress(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.mustPlanMoved(`
resource "mock_thing" "old" {
  for_each = toset(["a", "b"])
  name     = "x"
}`))
	pl := e.mustPlanMoved(`
resource "mock_thing" "new" {
  for_each = toset(["a", "b"])
  name     = "x"
  lifecycle {
    prevent_destroy = true
  }
}
moved {
  from = mock_thing.old
  to   = mock_thing.new
}`)
	if len(pl.Moves) != 2 {
		t.Fatalf("want both instances moved, got %v", pl.Moves)
	}
	e.apply(pl)

	_, err := e.planMoved(`
resource "mock_thing" "new" {
  for_each = toset(["b"])
  name     = "x"
  lifecycle {
    prevent_destroy = true
  }
}
moved {
  from = mock_thing.new["a"]
  to   = mock_thing.new["z"]
}`)
	if err == nil || !strings.Contains(err.Error(), `mock_thing.new["z"] has lifecycle.prevent_destroy set`) {
		t.Fatalf("want prevent_destroy to block the delete at the new address, got %v", err)
	}
}

// An import block targeting the move's destination is satisfied by the move
// when it names the same id: no import, no create.
func TestMovedWithImportAtDestination(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.mustPlanMoved(`
resource "mock_thing" "old" {
  name = "x"
}`))
	id := e.s.Resources[0].ID
	pl := e.mustPlanMoved(`
resource "mock_thing" "new" {
  name = "x"
}
moved {
  from = mock_thing.old
  to   = mock_thing.new
}
import {
  to = mock_thing.new
  id = "` + id + `"
}`)
	if s := pl.Summary(); s.Move != 1 || s.Import != 0 || s.Add != 0 {
		t.Fatalf("want just the move, got %+v\n%s", s, pl.RenderText(false))
	}
	e.apply(pl)
	if e.p.Creates() != 1 {
		t.Fatalf("creates = %d", e.p.Creates())
	}
	if r, ok := e.s.Find("mock_thing.new"); !ok || r.ID != id {
		t.Fatalf("moved entry = %+v, want id %s", r, id)
	}
}

// Dependencies recorded in state follow the move, on the moved resource and
// on resources that depend on it, and survive the apply.
func TestMovedRewritesRecordedDependencies(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.mustPlanMoved(`
resource "mock_thing" "set" {
  for_each = toset(["old"])
  name     = "x"
}
resource "mock_ref" "user" {
  name   = "u"
  target = mock_thing.set["old"].id
}`))
	pl := e.mustPlanMoved(`
resource "mock_thing" "set" {
  for_each = toset(["new"])
  name     = "x"
}
resource "mock_ref" "user" {
  name   = "u"
  target = mock_thing.set["new"].id
}
moved {
  from = mock_thing.set["old"]
  to   = mock_thing.set["new"]
}`)
	for _, c := range pl.Changes {
		if c.Action != provider.ActionNoop {
			t.Fatalf("want all no-ops after the move, got %s %s\n%s", c.Action, c.Address, pl.RenderText(false))
		}
	}
	e.apply(pl)
	persisted, err := e.b.Get()
	if err != nil {
		t.Fatal(err)
	}
	user, ok := persisted.Find("mock_ref.user")
	if !ok || !reflect.DeepEqual(user.Dependencies, []string{`mock_thing.set["new"]`}) {
		t.Fatalf("dependent's recorded dependencies = %+v", user)
	}
}

func TestMovedBothInStateFailsPlan(t *testing.T) {
	e := newForEachEnv(t)
	e.apply(e.mustPlanMoved(`
resource "mock_thing" "a" {
  name = "x"
}
resource "mock_thing" "b" {
  name = "y"
}`))
	_, err := e.planMoved(`
resource "mock_thing" "b" {
  name = "y"
}
moved {
  from = mock_thing.a
  to   = mock_thing.b
}`)
	if err == nil || !strings.Contains(err.Error(), "both mock_thing.a and mock_thing.b are in state") {
		t.Fatalf("want a both-in-state error, got %v", err)
	}
}
