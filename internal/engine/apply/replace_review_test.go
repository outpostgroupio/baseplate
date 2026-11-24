package apply

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

func single(typ, rev, kind string) []plan.DesiredResource {
	attrs := provider.Attributes{"name": cty.StringVal("a"), "rev": cty.StringVal(rev)}
	if kind != "" {
		attrs["kind"] = cty.StringVal(kind)
	}
	return []plan.DesiredResource{{Type: typ, Name: "a", Attributes: attrs}}
}

// Whether a content change is in place is decided by the CURRENT object, not
// the desired config: an object that isn't "inplace" today is replaced even if
// the config switches it to "inplace" (the roblox_asset Image -> Model case).
func TestInPlaceDecidedByCurrentObject(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, single("mock_replace", "1", "x"))
	c := changeFor(f.plan(t, single("mock_replace", "2", "inplace")), "mock_replace.a")
	if c.Action != provider.ActionReplace || strings.Join(c.Replace, ",") != "kind,rev" {
		t.Fatalf("want replace forced by kind and rev, got %s %v", c.Action, c.Replace)
	}
	// And the reverse: an "inplace" object switched away is replaced (kind),
	// never updated in place with a type it can't take.
	g := newReplaceFixture(t)
	g.apply(t, single("mock_replace", "1", "inplace"))
	if c := changeFor(g.plan(t, single("mock_replace", "1", "x")), "mock_replace.a"); c.Action != provider.ActionReplace {
		t.Fatalf("a kind change must replace, got %s", c.Action)
	}
}

// A replace of a resource that abandons its old object leaves it on the
// platform: no Delete, only dropped from state; plan says so.
func TestAbandonReplaceLeavesOldObject(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, single("mock_abandon", "1", ""))
	oldID := f.id(t, "mock_abandon.a")
	pl := f.plan(t, single("mock_abandon", "2", ""))
	c := changeFor(pl, "mock_abandon.a")
	if c.Action != provider.ActionReplace || !c.AbandonsOld {
		t.Fatalf("want an abandoning replace, got %s abandons=%v", c.Action, c.AbandonsOld)
	}
	out := pl.RenderText(false)
	if !strings.Contains(out, "# old object (id "+oldID+") is left in place and recorded for `baseplate cleanup`") ||
		!strings.Contains(out, "1 to add, 0 to change, 0 to destroy") {
		t.Fatalf("plan must say the old object is left in place:\n%s", out)
	}
	if s := pl.Summary(); s.Replace != 1 || s.Destroy != 0 {
		t.Fatalf("summary = %+v", s)
	}
	if j := pl.JSONChanges()[0]; !j.AbandonsOld || j.OldID != oldID {
		t.Fatalf("json = %+v", j)
	}
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.p.Store()[oldID]; !ok {
		t.Fatal("the old object must be left in place")
	}
	if f.id(t, "mock_abandon.a") == oldID {
		t.Fatal("state must point at the new object")
	}
}

// A missing trigger input (e.g. a source file absent in CI) must not fail plan
// or apply for a resource already in state: the recorded value is kept, with a
// warning.
func TestMissingTriggerInputKeepsRecordedValue(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, single("mock_replace", "1", ""))
	pl := f.plan(t, single("mock_replace", "<missing>", ""))
	if pl.HasChanges() {
		t.Fatalf("a missing input must not plan a change:\n%s", pl.RenderText(false))
	}
	w := strings.Join(pl.Warnings(), "\n")
	if !strings.Contains(w, "mock_replace.a: rev source not found; content changes can't be detected") {
		t.Fatalf("want a warning, got %q", w)
	}
	// An unrelated attribute change still applies, keeping the recorded rev.
	desired := single("mock_replace", "<missing>", "")
	desired[0].Attributes["name"] = cty.StringVal("renamed")
	f.apply(t, desired)
	st, _ := f.s.Find("mock_replace.a")
	if string(st.Triggers["rev"]) == "" || !strings.Contains(string(st.Triggers["rev"]), `"1"`) {
		t.Fatalf("the recorded rev must be kept, got %s", st.Triggers["rev"])
	}
	if got := f.p.Store()[st.ID]["name"].AsString(); got != "renamed" {
		t.Fatalf("name = %s", got)
	}
}

// Creating needs the trigger input; computing triggers happens before the
// remote create, so a failure leaves no orphaned object.
func TestTriggersComputedBeforeCreate(t *testing.T) {
	f := newReplaceFixture(t)
	pl := f.plan(t, single("mock_replace", "<missing>", ""))
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err == nil {
		t.Fatal("a create without its trigger input must fail")
	}
	if f.p.Creates() != 0 {
		t.Fatal("no remote object may be created when triggers can't be computed")
	}

	g := newReplaceFixture(t)
	g.apply(t, single("mock_replace", "1", ""))
	rpl := g.plan(t, single("mock_replace", "2", ""))
	rpl.Changes[0].Desired = provider.Attributes{"name": cty.StringVal("a"), "rev": cty.StringVal("<missing>")}
	creates := g.p.Creates()
	if err := Apply(g.ctx, g.p, g.b, g.s, rpl); err == nil {
		t.Fatal("replace must fail when triggers can't be computed")
	}
	if g.p.Creates() != creates {
		t.Fatal("replace must compute triggers before creating the successor")
	}
}

// A reference held in a Computed (create-only) attribute still makes its
// holder update when the target gets a new id, so the new id reaches state.
func TestDependentRefInComputedAttrIsPlanned(t *testing.T) {
	f := newReplaceFixture(t)
	desired := func(rev string) []plan.DesiredResource {
		return append(single("mock_replace", rev, ""), plan.DesiredResource{
			Type: "mock_computed", Name: "c", DependsOn: []string{"mock_replace.a"},
			Attributes: provider.Attributes{"name": cty.StringVal("c")},
			Refs:       map[string]provider.Ref{"server_id": {Address: "mock_replace.a", Attr: "id"}},
		})
	}
	f.apply(t, desired("1"))
	pl := f.plan(t, desired("2"))
	if c := changeFor(pl, "mock_computed.c"); c.Action != provider.ActionUpdate {
		t.Fatalf("the dependent must be updated, got %s", c.Action)
	}
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	if got := f.p.Store()[f.id(t, "mock_computed.c")]["server_id"].AsString(); got != f.id(t, "mock_replace.a") {
		t.Fatalf("server_id = %s, want the new id %s", got, f.id(t, "mock_replace.a"))
	}
}

// Import targets: a dependent already in state is planned against the
// imported id when the import adopts as-is (no spurious update), and against
// an unknown id when the import is then replaced.
func TestImportTargetDependents(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		want provider.ChangeAction
	}{
		{"adopted as-is", "x", provider.ActionNoop},
		{"imported then replaced", "y", provider.ActionUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReplaceFixture(t)
			f.p.Seed("remote-1", provider.Attributes{"name": cty.StringVal("a"), "kind": cty.StringVal("x")})
			desired := []plan.DesiredResource{
				{Type: "mock_replace", Name: "a", Attributes: provider.Attributes{
					"name": cty.StringVal("a"), "rev": cty.StringVal("1"), "kind": cty.StringVal(tc.kind)}},
				{Type: "mock_ref", Name: "b", DependsOn: []string{"mock_replace.a"},
					Attributes: provider.Attributes{"name": cty.StringVal("b")},
					Refs:       map[string]provider.Ref{"target": {Address: "mock_replace.a", Attr: "id"}}},
			}
			// b is in state already, pointing at the object about to be imported.
			id, out, err := f.p.Resources()["mock_ref"].Create(f.ctx, provider.Attributes{"name": cty.StringVal("b"), "target": cty.StringVal("remote-1")})
			if err != nil {
				t.Fatal(err)
			}
			if err := commit(f.b, f.s, stateResource(plan.Change{Type: "mock_ref", Name: "b"}, id, out, nil)); err != nil {
				t.Fatal(err)
			}
			pl := f.plan(t, desired)
			if err := plan.ResolveImports(f.ctx, f.p, f.s, pl, []plan.ImportBlock{{Address: "mock_replace.a", ID: "remote-1"}}); err != nil {
				t.Fatal(err)
			}
			if c := changeFor(pl, "mock_ref.b"); c.Action != tc.want {
				t.Fatalf("dependent action = %s, want %s\n%s", c.Action, tc.want, pl.RenderText(false))
			}
			if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
				t.Fatal(err)
			}
			if got := f.p.Store()[f.id(t, "mock_ref.b")]["target"].AsString(); got != f.id(t, "mock_replace.a") {
				t.Fatalf("target = %s, want %s", got, f.id(t, "mock_replace.a"))
			}
			if again := f.plan(t, desired); again.HasChanges() {
				t.Fatalf("must converge:\n%s", again.RenderText(false))
			}
		})
	}
}

// Updates of a TriggerAwareUpdater get the triggers recorded before the update.
func TestUpdatePassesPriorTriggers(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, single("mock_replace", "1", "inplace"))
	pl := f.apply(t, single("mock_replace", "2", "inplace"))
	if c := changeFor(pl, "mock_replace.a"); c.Action != provider.ActionUpdate {
		t.Fatalf("want update, got %s", c.Action)
	}
	if got := f.p.LastPrior()["rev"]; got.IsNull() || got.AsString() != "1" {
		t.Fatalf("prior rev = %#v, want 1", got)
	}
}

// Only triggers plan flagged as lacking a baseline are backfilled; a
// converged resource with a full baseline isn't flagged.
func TestBackfillOnlyFlaggedTriggers(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, single("mock_replace", "1", ""))
	if c := changeFor(f.plan(t, single("mock_replace", "1", "")), "mock_replace.a"); len(c.BackfillTriggers) != 0 {
		t.Fatalf("nothing to backfill, got %v", c.BackfillTriggers)
	}
	st, _ := f.s.Find("mock_replace.a")
	st.Triggers = nil
	if c := changeFor(f.plan(t, single("mock_replace", "1", "")), "mock_replace.a"); strings.Join(c.BackfillTriggers, ",") != "rev" {
		t.Fatalf("want rev flagged for backfill, got %v", c.BackfillTriggers)
	}
}

// A moved instance whose trigger also changed shows the move, then a replace
// at the new address.
func TestMoveThenReplaceCompose(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, single("mock_replace", "1", ""))
	oldID := f.id(t, "mock_replace.a")
	moves, err := plan.ApplyMoves(f.s, []plan.MovedBlock{{From: "mock_replace.a", To: "mock_replace.b"}})
	if err != nil {
		t.Fatal(err)
	}
	desired := single("mock_replace", "2", "")
	desired[0].Name = "b"
	pl := f.plan(t, desired)
	pl.Moves = moves
	c := changeFor(pl, "mock_replace.b")
	if c.Action != provider.ActionReplace || c.ID != oldID {
		t.Fatalf("want a replace of the moved object at the new address, got %s (id %s)", c.Action, c.ID)
	}
	out := pl.RenderText(false)
	mi, ri := strings.Index(out, "~ mock_replace.a has moved to mock_replace.b"), strings.Index(out, "-/+ mock_replace.b (replace)")
	if mi < 0 || ri < 0 || mi > ri || !strings.Contains(out, "1 to add, 0 to change, 1 to destroy, 1 to move.") {
		t.Fatalf("want the move, then the replace:\n%s", out)
	}
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.s.Find("mock_replace.a"); ok {
		t.Fatal("the old address must be gone")
	}
	if id := f.id(t, "mock_replace.b"); id == oldID {
		t.Fatal("the moved object must be replaced")
	}
}
