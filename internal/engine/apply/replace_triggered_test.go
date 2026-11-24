package apply

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

// The maps pattern: a baseplate_data per map holds its source hash, and the
// map lists it in replace_triggered_by, so a new hash replaces the map (a new
// id) while the old object is left in place and recorded for cleanup.
func triggeredDesired(hash string) []plan.DesiredResource {
	return []plan.DesiredResource{
		{Type: "baseplate_data", Name: "hash", Key: "m", Attributes: provider.Attributes{"input": cty.StringVal(hash)}},
		{Type: "mock_abandon", Name: "map", Key: "m", DependsOn: []string{`baseplate_data.hash["m"]`},
			Attributes:         provider.Attributes{"name": cty.StringVal("m"), "rev": cty.StringVal("1")},
			ReplaceTriggeredBy: []provider.Ref{{Address: `baseplate_data.hash["m"]`}}},
	}
}

func TestReplaceTriggeredByData(t *testing.T) {
	f := newReplaceFixture(t)
	f.apply(t, triggeredDesired("h1"))
	oldID := f.id(t, `mock_abandon.map["m"]`)

	if pl := f.plan(t, triggeredDesired("h1")); pl.HasChanges() {
		t.Fatalf("baseplate_data must converge:\n%s", pl.RenderText(false))
	}

	pl := f.plan(t, triggeredDesired("h2"))
	c := changeFor(pl, `mock_abandon.map["m"]`)
	if c.Action != provider.ActionReplace || strings.Join(c.TriggeredBy, ",") != `baseplate_data.hash["m"]` {
		t.Fatalf("want a replace triggered by the data, got %s %v", c.Action, c.TriggeredBy)
	}
	if out := pl.RenderText(false); !strings.Contains(out, `# replace_triggered_by: baseplate_data.hash["m"] changed`) {
		t.Fatalf("plan must say why:\n%s", out)
	}
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	newID := f.id(t, `mock_abandon.map["m"]`)
	if newID == oldID {
		t.Fatal("the map must get a new id")
	}
	if _, ok := f.p.Store()[oldID]; !ok {
		t.Fatal("the old map must be left in place")
	}
	if len(f.s.Retired) != 1 || f.s.Retired[0].ID != oldID || f.s.Retired[0].ReplacedBy != newID || f.s.Retired[0].RetiredAt.IsZero() {
		t.Fatalf("the old map must be recorded for cleanup: %+v", f.s.Retired)
	}
	if pl := f.plan(t, triggeredDesired("h2")); pl.HasChanges() {
		t.Fatalf("must converge after the replace:\n%s", pl.RenderText(false))
	}
}

// Adding the trigger resource to existing objects replaces nothing: a data
// resource being created doesn't fire.
func TestReplaceTriggeredByIgnoresCreate(t *testing.T) {
	f := newReplaceFixture(t)
	d := triggeredDesired("h1")
	f.apply(t, []plan.DesiredResource{{Type: d[1].Type, Name: d[1].Name, Key: d[1].Key, Attributes: d[1].Attributes}})
	pl := f.plan(t, d)
	if c := changeFor(pl, `mock_abandon.map["m"]`); c.Action != provider.ActionNoop {
		t.Fatalf("creating the trigger must not replace, got %s", c.Action)
	}
}

// Adopting maps whose hashes aren't in state yet: importing the baseplate_data
// takes its values from config, so it plans no change and nothing is replaced,
// and later hash changes trigger as usual.
func TestImportData(t *testing.T) {
	f := newReplaceFixture(t)
	d := triggeredDesired("h1")
	f.apply(t, []plan.DesiredResource{{Type: d[1].Type, Name: d[1].Name, Key: d[1].Key, Attributes: d[1].Attributes}})

	pl := f.plan(t, d)
	if err := plan.ResolveImports(f.ctx, f.p, f.s, pl, []plan.ImportBlock{{Address: `baseplate_data.hash["m"]`, ID: "maps/m"}}); err != nil {
		t.Fatal(err)
	}
	if c := changeFor(pl, `baseplate_data.hash["m"]`); c.Action != provider.ActionNoop || c.ImportID != "maps/m" {
		t.Fatalf("want an import with no change, got %s %q", c.Action, c.ImportID)
	}
	if got, want := pl.Summary().Applied(), "1 imported, 0 added, 0 changed, 0 destroyed"; got != want {
		t.Fatalf("Applied() = %q, want %q", got, want)
	}
	if err := Apply(f.ctx, f.p, f.b, f.s, pl); err != nil {
		t.Fatal(err)
	}
	if id := f.id(t, `baseplate_data.hash["m"]`); id != "maps/m" {
		t.Fatalf("the import id must be the id, got %q", id)
	}
	if pl := f.plan(t, d); pl.HasChanges() {
		t.Fatalf("must converge after the import:\n%s", pl.RenderText(false))
	}
	if c := changeFor(f.plan(t, triggeredDesired("h2")), `mock_abandon.map["m"]`); c.Action != provider.ActionReplace {
		t.Fatalf("an imported data must still trigger, got %s", c.Action)
	}
}

func TestImportDataCommand(t *testing.T) {
	f := newReplaceFixture(t)
	if err := Import(f.ctx, f.p, f.b, f.s, `baseplate_data.hash["m"]`, "maps/m", triggeredDesired("h1")); err != nil {
		t.Fatal(err)
	}
	r, ok := f.s.Find(`baseplate_data.hash["m"]`)
	if !ok || r.ID != "maps/m" {
		t.Fatalf("want baseplate_data imported as maps/m, got %+v", r)
	}
	if pl := f.plan(t, triggeredDesired("h1")[:1]); pl.HasChanges() {
		t.Fatalf("an imported data must match config:\n%s", pl.RenderText(false))
	}
}
