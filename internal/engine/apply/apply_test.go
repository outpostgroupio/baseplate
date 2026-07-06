package apply

import (
	"context"
	"path/filepath"
	"strings"
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

// TestReplanReconstructsCollectionReference proves the state wire format
// round-trips a non-string reference with its exact cty type. State is written
// with Marshal(v, cty.DynamicPseudoType) (a {value,type} wrapper) and read with
// Unmarshal(_, DynamicPseudoType), so plan reconstructs the referenced list as a
// list. Before the fix, state was written bare (Marshal(v, v.Type())) but read
// against DynamicPseudoType, which errors and silently DROPS the attribute from
// the plan's comparison — so drift on a referenced collection went undetected.
// This test asserts both halves: (a) convergence when nothing changed, and
// (b) drift detection when the stored value differs (the part that fails without
// the fix, because a dropped attribute is never compared).
func TestReplanReconstructsCollectionReference(t *testing.T) {
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

	// (a) Nothing changed: re-plan converges.
	second, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasChanges() {
		t.Fatalf("re-plan with an unchanged collection reference must converge, got: %s", second.RenderText(false))
	}

	// (b) Drift the referring resource's stored target, then re-plan MUST detect
	// it — proving the referenced value is actually reconstructed from state and
	// compared, not silently dropped. Find mock_ref.b's real id from the store.
	var refID string
	for id, a := range p.Store() {
		if v, ok := a["target"]; ok && v.Type().IsListType() {
			refID = id
		}
	}
	if refID == "" {
		t.Fatal("mock_ref.b (with a list target) not found in store")
	}
	drifted := cty.ListVal([]cty.Value{cty.StringVal("x"), cty.StringVal("DRIFTED")})
	p.Seed(refID, provider.Attributes{"name": cty.StringVal("child"), "target": drifted})

	third, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !third.HasChanges() {
		t.Fatal("re-plan must detect drift on the referenced collection attribute, but reported no changes (attribute was silently dropped)")
	}
}

// TestTriggerChangeForcesUpdateAndConverges proves the Triggers capability:
// a mock_trigger resource's "rev" is Computed (excluded from the attr diff), so
// only the trigger can drive an update. Same rev converges (incl. a disk reload);
// a changed rev forces an Update even though no declared attribute differs.
func TestTriggerChangeForcesUpdateAndConverges(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	rev1 := []plan.DesiredResource{
		{Type: "mock_trigger", Name: "a", Attributes: provider.Attributes{
			"name": cty.StringVal("t"), "rev": cty.StringVal("1")}},
	}
	first, err := plan.Build(ctx, p, s, rev1)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, first); err != nil {
		t.Fatal(err)
	}

	// Same rev, planned against the RELOADED-from-disk state → converges
	// (proves triggers round-trip through the state file).
	reloaded, err := b.Get()
	if err != nil {
		t.Fatal(err)
	}
	same, err := plan.Build(ctx, p, reloaded, rev1)
	if err != nil {
		t.Fatal(err)
	}
	if same.HasChanges() {
		t.Fatalf("an unchanged trigger must converge, got: %s", same.RenderText(false))
	}

	// Changed rev → Update, even though rev is Computed (no attr diff).
	rev2 := []plan.DesiredResource{
		{Type: "mock_trigger", Name: "a", Attributes: provider.Attributes{
			"name": cty.StringVal("t"), "rev": cty.StringVal("2")}},
	}
	second, err := plan.Build(ctx, p, reloaded, rev2)
	if err != nil {
		t.Fatal(err)
	}
	if !second.HasChanges() {
		t.Fatal("a changed trigger must force an Update")
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

func TestImportAdoptsExistingResourceAndConverges(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("mock_thing/7", provider.Attributes{"name": cty.StringVal("adopted")})
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("adopted")}},
	}
	if err := Import(ctx, p, b, s, "mock_thing.a", "mock_thing/7", desired); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Find("mock_thing.a")
	if !ok || got.ID != "mock_thing/7" {
		t.Fatalf("import did not record the resource with the right id: %+v", got)
	}
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("plan after import must converge, got: %s", pl.RenderText(false))
	}
}

func TestImportRecordsTriggersAndConverges(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("t1", provider.Attributes{"name": cty.StringVal("x"), "rev": cty.StringVal("v1")})
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	desired := []plan.DesiredResource{
		{Type: "mock_trigger", Name: "a", Attributes: provider.Attributes{
			"name": cty.StringVal("x"), "rev": cty.StringVal("v1")}},
	}
	if err := Import(ctx, p, b, s, "mock_trigger.a", "t1", desired); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Find("mock_trigger.a")
	if len(got.Triggers) == 0 {
		t.Fatal("import must record triggers for a triggerable resource")
	}
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("plan after triggerable import must converge, got: %s", pl.RenderText(false))
	}
}

func TestImportErrorsWhenNotDeclared(t *testing.T) {
	p := mock.New()
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.tfstate"))
	err := Import(context.Background(), p, b, s, "mock_thing.ghost", "x", nil)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("want a not-declared error, got %v", err)
	}
}

func TestImportErrorsWhenAlreadyInState(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("id1", provider.Attributes{"name": cty.StringVal("x")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "id1"})
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.tfstate"))
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	}
	err := Import(ctx, p, b, s, "mock_thing.a", "id1", desired)
	if err == nil || !strings.Contains(err.Error(), "already in state") {
		t.Fatalf("want an already-in-state error, got %v", err)
	}
}

func TestImportErrorsWhenRemoteNotFound(t *testing.T) {
	ctx := context.Background()
	p := mock.New() // empty store → Read returns provider.ErrNotFound
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.tfstate"))
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	}
	err := Import(ctx, p, b, s, "mock_thing.a", "missing", desired)
	if err == nil || !strings.Contains(err.Error(), "no mock_thing found") {
		t.Fatalf("want a not-found error, got %v", err)
	}
}

func TestApplyImportBlockAdoptsAndReconciles(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("mock_thing/7", provider.Attributes{"name": cty.StringVal("old")})
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("new")}},
	}
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ResolveImports(ctx, p, s, pl, []plan.ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/7"}}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	// Adopted into state under the real id, and reconciled to config ("new").
	got, ok := s.Find("mock_thing.a")
	if !ok || got.ID != "mock_thing/7" {
		t.Fatalf("import did not adopt under the real id: %+v", got)
	}
	if p.Store()["mock_thing/7"]["name"].AsString() != "new" {
		t.Fatalf("one-pass import must reconcile the remote to config, store = %+v", p.Store()["mock_thing/7"])
	}
	// Second plan converges.
	pl2, _ := plan.Build(ctx, p, s, desired)
	if err := plan.ResolveImports(ctx, p, s, pl2, []plan.ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/7"}}); err != nil {
		t.Fatal(err)
	}
	if pl2.HasChanges() {
		t.Fatalf("post-import plan must converge, got: %s", pl2.RenderText(false))
	}
}

func TestApplyImportBlockTriggerableAdoptsCleanly(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("t1", provider.Attributes{"name": cty.StringVal("x"), "rev": cty.StringVal("v1")})
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))

	desired := []plan.DesiredResource{
		{Type: "mock_trigger", Name: "a", Attributes: provider.Attributes{
			"name": cty.StringVal("x"), "rev": cty.StringVal("v1")}},
	}
	pl, _ := plan.Build(ctx, p, s, desired)
	if err := plan.ResolveImports(ctx, p, s, pl, []plan.ImportBlock{{Address: "mock_trigger.a", ID: "t1"}}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Find("mock_trigger.a")
	if len(got.Triggers) == 0 {
		t.Fatal("import must record triggers (baseline from config) for a triggerable resource")
	}
	pl2, _ := plan.Build(ctx, p, s, desired)
	if err := plan.ResolveImports(ctx, p, s, pl2, []plan.ImportBlock{{Address: "mock_trigger.a", ID: "t1"}}); err != nil {
		t.Fatal(err)
	}
	if pl2.HasChanges() {
		t.Fatalf("triggerable import must converge, got: %s", pl2.RenderText(false))
	}
}
