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

// mock_publish's "rev" trigger can't be read back (like a place's content), so
// an import must not claim the live object matches the config.

func publishFixture(t *testing.T) (*mock.Provider, *state.State, backend.Backend, []plan.DesiredResource) {
	t.Helper()
	p := mock.New()
	p.Seed("p1", provider.Attributes{"name": cty.StringVal("x")})
	desired := []plan.DesiredResource{{Type: "mock_publish", Name: "a", Attributes: provider.Attributes{
		"name": cty.StringVal("x"), "rev": cty.StringVal("v1")}}}
	return p, state.New(), backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate")), desired
}

func TestImportMarksUnreadableTriggersUnverified(t *testing.T) {
	p, s, b, desired := publishFixture(t)
	ctx, warns := provider.WithWarnings(context.Background())
	if err := Import(ctx, p, b, s, "mock_publish.a", "p1", desired); err != nil {
		t.Fatal(err)
	}
	if w := warns.List(); len(w) != 1 || !strings.Contains(w[0], "--assume-current") {
		t.Fatalf("import must warn that the content is unverified, got %v", w)
	}
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 1 || pl.Changes[0].Action != provider.ActionUpdate {
		t.Fatalf("the plan after import must update the unverified content, got:\n%s", pl.RenderText(false))
	}
	if got := pl.RenderText(false); !strings.Contains(got, "rev: "+provider.UnverifiedTrigger+" => v1") {
		t.Fatalf("the update must show the unverified trigger:\n%s", got)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	pl, err = plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("plan must converge after the update:\n%s", pl.RenderText(false))
	}
}

func TestImportAssumeCurrentAdoptsUnreadableTriggers(t *testing.T) {
	p, s, b, desired := publishFixture(t)
	if err := ImportWithOptions(context.Background(), p, b, s, "mock_publish.a", "p1", desired, ImportOptions{AssumeCurrent: true}); err != nil {
		t.Fatal(err)
	}
	pl, err := plan.Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("--assume-current must converge without an update:\n%s", pl.RenderText(false))
	}
}

func TestImportBlockPlansUpdateForUnreadableTriggers(t *testing.T) {
	p, s, b, desired := publishFixture(t)
	ctx := context.Background()
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ResolveImports(ctx, p, s, pl, []plan.ImportBlock{{Address: "mock_publish.a", ID: "p1"}}); err != nil {
		t.Fatal(err)
	}
	c := pl.Changes[0]
	if c.ImportID != "p1" || c.Action != provider.ActionUpdate {
		t.Fatalf("want import then update, got %s:\n%s", c.Action, pl.RenderText(false))
	}
	if got := pl.RenderText(false); !strings.Contains(got, "will be imported (id p1), then updated") {
		t.Fatalf("render must say the import is followed by an update:\n%s", got)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	pl, err = plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("plan must converge after import + update:\n%s", pl.RenderText(false))
	}
}

func TestImportAllRecordsPendingBlocksInOneWrite(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("a")})
	p.Seed("mock_thing/2", provider.Attributes{"name": cty.StringVal("b")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "a", ID: "mock_thing/1"})
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "set", Key: "a", Attributes: provider.Attributes{"name": cty.StringVal("a")}},
		{Type: "mock_thing", Name: "set", Key: "b", Attributes: provider.Attributes{"name": cty.StringVal("b")}},
	}
	imports := []plan.ImportBlock{
		{Address: `mock_thing.set["a"]`, ID: "mock_thing/1"},
		{Address: `mock_thing.set["b"]`, ID: "mock_thing/2"},
	}
	serial := s.Serial
	done, err := ImportAll(context.Background(), p, b, s, imports, desired, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].Address != `mock_thing.set["b"]` {
		t.Fatalf("only the pending block should be imported, got %+v", done)
	}
	if s.Serial != serial+1 {
		t.Fatalf("state should be written once, serial %d -> %d", serial, s.Serial)
	}
	pl, err := plan.Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ResolveImports(context.Background(), p, s, pl, imports); err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("plan must be clean after importing every block:\n%s", pl.RenderText(false))
	}
	if p.Creates() != 0 {
		t.Fatal("import must never create remote objects")
	}
}

func TestImportAllWritesNothingWhenOneBlockFails(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("a")})
	s := state.New()
	path := filepath.Join(t.TempDir(), "s.bpstate")
	b := backend.NewLocal(path)
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("a")}},
		{Type: "mock_thing", Name: "b", Attributes: provider.Attributes{"name": cty.StringVal("b")}},
	}
	_, err := ImportAll(context.Background(), p, b, s, []plan.ImportBlock{
		{Address: "mock_thing.a", ID: "mock_thing/1"},
		{Address: "mock_thing.b", ID: "missing"},
	}, desired, ImportOptions{})
	if err == nil || !strings.Contains(err.Error(), "import mock_thing.b") {
		t.Fatalf("want an error naming the failed block, got %v", err)
	}
	persisted, err := b.Get()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Resources) != 0 {
		t.Fatalf("a failed import must write nothing, got %+v", persisted.Resources)
	}
}
