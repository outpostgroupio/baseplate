package apply

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// replaceFixture is mock_replace.a (a change to "rev", a trigger, or "kind", an
// attribute, forces a replace) plus mock_ref.b, whose "target" references a.id.
type replaceFixture struct {
	ctx context.Context
	p   *mock.Provider
	s   *state.State
	b   backend.Backend
}

func newReplaceFixture(t *testing.T) *replaceFixture {
	t.Helper()
	return &replaceFixture{
		ctx: context.Background(),
		p:   mock.New(),
		s:   state.New(),
		b:   backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.bpstate")),
	}
}

func replaceDesired(rev, kind string, withRef bool) []plan.DesiredResource {
	attrs := provider.Attributes{"name": cty.StringVal("a"), "rev": cty.StringVal(rev)}
	if kind != "" {
		attrs["kind"] = cty.StringVal(kind)
	}
	out := []plan.DesiredResource{{Type: "mock_replace", Name: "a", Attributes: attrs}}
	if withRef {
		out = append(out, plan.DesiredResource{
			Type: "mock_ref", Name: "b", DependsOn: []string{"mock_replace.a"},
			Attributes: provider.Attributes{"name": cty.StringVal("b")},
			Refs:       map[string]provider.Ref{"target": {Address: "mock_replace.a", Attr: "id"}},
		})
	}
	return out
}

func (f *replaceFixture) plan(t *testing.T, desired []plan.DesiredResource) *plan.Plan {
	t.Helper()
	pl, err := plan.Build(f.ctx, f.p, f.s, desired)
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

func (f *replaceFixture) apply(t *testing.T, desired []plan.DesiredResource) *plan.Plan {
	t.Helper()
	pl := f.plan(t, desired)
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	return pl
}

func (f *replaceFixture) id(t *testing.T, address string) string {
	t.Helper()
	r, ok := f.s.Find(address)
	if !ok {
		t.Fatalf("%s not in state", address)
	}
	return r.ID
}

func changeFor(pl *plan.Plan, address string) plan.Change {
	for _, c := range pl.Changes {
		if c.Address == address {
			return c
		}
	}
	return plan.Change{}
}

func TestReplaceOnTriggerChangeCreatesNewObjectAndConverges(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, replaceDesired("1", "", false))
	oldID := f.id(t, "mock_replace.a")

	pl := f.plan(t, replaceDesired("2", "", false))
	c := changeFor(pl, "mock_replace.a")
	if c.Action != provider.ActionReplace || !reflect.DeepEqual(c.Replace, []string{"rev"}) {
		t.Fatalf("want replace forced by rev, got %s %v", c.Action, c.Replace)
	}
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	newID := f.id(t, "mock_replace.a")
	if newID == oldID {
		t.Fatal("a replace must record the new object's id")
	}
	store := f.p.Store()
	if _, ok := store[oldID]; ok {
		t.Fatal("the old object must be deleted after the replace")
	}
	if _, ok := store[newID]; !ok {
		t.Fatal("the new object must exist")
	}
	persisted, _ := f.b.Get()
	if r, _ := persisted.Find("mock_replace.a"); r == nil || r.ID != newID {
		t.Fatal("the new id must be persisted")
	}
	if again := f.plan(t, replaceDesired("2", "", false)); again.HasChanges() {
		t.Fatalf("re-plan after a replace must converge, got:\n%s", again.RenderText(false))
	}
}

func TestReplaceOnAttributeChange(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, replaceDesired("1", "x", false))
	c := changeFor(f.plan(t, replaceDesired("1", "y", false)), "mock_replace.a")
	if c.Action != provider.ActionReplace || !reflect.DeepEqual(c.Replace, []string{"kind"}) {
		t.Fatalf("want replace forced by kind, got %s %v", c.Action, c.Replace)
	}
}

// RequiresReplace depends on config: with kind = "inplace", a rev change is an
// ordinary in-place update (the roblox_asset Model case).
func TestNonForcingTriggerChangeIsAnUpdate(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, replaceDesired("1", "inplace", false))
	oldID := f.id(t, "mock_replace.a")
	pl := f.apply(t, replaceDesired("2", "inplace", false))
	if c := changeFor(pl, "mock_replace.a"); c.Action != provider.ActionUpdate {
		t.Fatalf("want update, got %s", c.Action)
	}
	if f.id(t, "mock_replace.a") != oldID {
		t.Fatal("an in-place update must keep the id")
	}
}

// A dependent that references the replaced resource's id is planned as an
// update to a value known after apply, and gets the new id in the same apply.
func TestReplaceUpdatesDependentReferenceInSameApply(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, replaceDesired("1", "", true))

	pl := f.plan(t, replaceDesired("2", "", true))
	dep := changeFor(pl, "mock_ref.b")
	if dep.Action != provider.ActionUpdate {
		t.Fatalf("the dependent must be updated, got %s", dep.Action)
	}
	if len(dep.AttrDiffs) != 1 || dep.AttrDiffs[0].Name != "target" || dep.AttrDiffs[0].New.IsKnown() {
		t.Fatalf("want target => (known after apply), got %+v", dep.AttrDiffs)
	}
	if out := pl.RenderText(false); !strings.Contains(out, "target = (known after apply)") {
		t.Fatalf("render should show the pending reference:\n%s", out)
	}
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	newID := f.id(t, "mock_replace.a")
	got := f.p.Store()[f.id(t, "mock_ref.b")]["target"]
	if got.AsString() != newID {
		t.Fatalf("dependent target = %s, want the new id %s", got.AsString(), newID)
	}
	if again := f.plan(t, replaceDesired("2", "", true)); again.HasChanges() {
		t.Fatalf("must converge, got:\n%s", again.RenderText(false))
	}
}

// A dependent of a resource that is re-created because it vanished remotely
// also gets the new id (the same pending-reference rule).
func TestRecreateAfterDriftUpdatesDependentReference(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, replaceDesired("1", "", true))
	if err := f.p.Resources()["mock_replace"].Delete(f.ctx, f.id(t, "mock_replace.a")); err != nil {
		t.Fatal(err)
	}
	pl := f.apply(t, replaceDesired("1", "", true))
	if c := changeFor(pl, "mock_ref.b"); c.Action != provider.ActionUpdate {
		t.Fatalf("dependent of a re-created resource must update, got %s", c.Action)
	}
	if got := f.p.Store()[f.id(t, "mock_ref.b")]["target"].AsString(); got != f.id(t, "mock_replace.a") {
		t.Fatalf("dependent target = %s, want %s", got, f.id(t, "mock_replace.a"))
	}
}

func TestReplaceCreateFailureLeavesCurrentObject(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, replaceDesired("1", "", false))
	oldID := f.id(t, "mock_replace.a")
	f.p.FailNext("create", errors.New("upload rejected"))
	err := Apply(f.ctx, f.p, f.b, f.s, f.plan(t, replaceDesired("2", "", false)))
	if err == nil || !strings.Contains(err.Error(), "upload rejected") {
		t.Fatalf("want the create error, got %v", err)
	}
	if f.id(t, "mock_replace.a") != oldID {
		t.Fatal("a failed create must leave state on the current object")
	}
	if _, ok := f.p.Store()[oldID]; !ok {
		t.Fatal("a failed create must not delete the current object")
	}
}

func TestReplaceDeleteFailureKeepsNewObjectInState(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, replaceDesired("1", "", false))
	oldID := f.id(t, "mock_replace.a")
	f.p.FailNext("delete", errors.New("boom"))
	err := Apply(f.ctx, f.p, f.b, f.s, f.plan(t, replaceDesired("2", "", false)))
	if err == nil || !strings.Contains(err.Error(), oldID) {
		t.Fatalf("want an error naming the orphaned old id %s, got %v", oldID, err)
	}
	if f.id(t, "mock_replace.a") == oldID {
		t.Fatal("state must point at the new object once it exists")
	}
	persisted, _ := f.b.Get()
	if r, _ := persisted.Find("mock_replace.a"); r == nil || r.ID == oldID {
		t.Fatal("the new id must be persisted before the old object is deleted")
	}
}

// State written before a resource had its replace-forcing trigger has no
// recorded value: plan must not replace (adopt as baseline), apply records the
// baseline, and a later real change does replace.
func TestMissingTriggerBaselineIsAdoptedNotReplaced(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, replaceDesired("1", "", false))
	st, _ := f.s.Find("mock_replace.a")
	st.Triggers = nil // as if written by an older version

	pl := f.plan(t, replaceDesired("1", "", false))
	if pl.HasChanges() {
		t.Fatalf("a missing baseline must not force a replace, got:\n%s", pl.RenderText(false))
	}
	serial := f.s.Serial
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.s.Find("mock_replace.a"); st.Triggers["rev"] == nil {
		t.Fatal("apply must record the trigger baseline")
	}
	if f.s.Serial == serial {
		t.Fatal("the baseline must be written to state")
	}
	// Converged apply doesn't churn the serial.
	serial = f.s.Serial
	f.apply(t, replaceDesired("1", "", false))
	if f.s.Serial != serial {
		t.Fatal("a converged apply must not rewrite state")
	}
	if c := changeFor(f.plan(t, replaceDesired("2", "", false)), "mock_replace.a"); c.Action != provider.ActionReplace {
		t.Fatalf("after the baseline, a change must replace; got %s", c.Action)
	}
}

// Import (CLI and import block) records the trigger baseline from config, so
// the next plan doesn't replace an adopted object.
func TestImportDoesNotForceReplace(t *testing.T) {
	f := newReplaceFixture(t)
	f.p.Seed("remote-1", provider.Attributes{"name": cty.StringVal("a"), "rev": cty.StringVal("old")})
	desired := replaceDesired("1", "", false)
	if err := Import(f.ctx, f.p, f.b, f.s, "mock_replace.a", "remote-1", desired); err != nil {
		t.Fatal(err)
	}
	if pl := f.plan(t, desired); pl.HasChanges() {
		t.Fatalf("plan after import must not replace, got:\n%s", pl.RenderText(false))
	}

	g := newReplaceFixture(t)
	g.p.Seed("remote-2", provider.Attributes{"name": cty.StringVal("a"), "rev": cty.StringVal("old")})
	pl := g.plan(t, desired)
	if err := plan.ResolveImports(g.ctx, g.p, g.s, pl, []plan.ImportBlock{{Address: "mock_replace.a", ID: "remote-2"}}); err != nil {
		t.Fatal(err)
	}
	if c := changeFor(pl, "mock_replace.a"); c.Action == provider.ActionReplace {
		t.Fatal("an import block must not plan a replace on a trigger")
	}
	if err := Apply(g.ctx, g.p, g.b, g.s, pl); err != nil {
		t.Fatal(err)
	}
	if again := g.plan(t, desired); again.HasChanges() {
		t.Fatalf("plan after an import block must converge, got:\n%s", again.RenderText(false))
	}
	if g.id(t, "mock_replace.a") != "remote-2" || g.p.Creates() != 0 {
		t.Fatal("import must adopt, not create")
	}
}

// An import whose declared attribute can't be reconciled in place is imported
// and then replaced in the same apply.
func TestImportBlockThenReplaceOnForcingAttribute(t *testing.T) {
	f := newReplaceFixture(t)
	f.p.Seed("remote-1", provider.Attributes{"name": cty.StringVal("a"), "kind": cty.StringVal("x")})
	desired := replaceDesired("1", "y", false)
	pl := f.plan(t, desired)
	if err := plan.ResolveImports(f.ctx, f.p, f.s, pl, []plan.ImportBlock{{Address: "mock_replace.a", ID: "remote-1"}}); err != nil {
		t.Fatal(err)
	}
	c := changeFor(pl, "mock_replace.a")
	if c.Action != provider.ActionReplace || c.ImportID != "remote-1" {
		t.Fatalf("want import then replace, got %s (import %q)", c.Action, c.ImportID)
	}
	if s := pl.Summary(); s.Import != 1 || s.Add != 1 || s.Destroy != 1 || s.Replace != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	if id := f.id(t, "mock_replace.a"); id == "remote-1" {
		t.Fatal("the adopted object must be replaced")
	}
	if _, ok := f.p.Store()["remote-1"]; ok {
		t.Fatal("the adopted object must be deleted by the replace")
	}
}
