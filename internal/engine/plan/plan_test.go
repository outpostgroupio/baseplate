package plan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
)

func TestBuildCreateAndOrder(t *testing.T) {
	p := mock.New()
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_child", Name: "c", Attributes: provider.Attributes{
			"name": cty.StringVal("child"), "parent": cty.StringVal("mock_thing.a")},
			DependsOn: []string{"mock_thing.a"}},
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !pl.HasChanges() {
		t.Fatal("expected changes")
	}
	if pl.Changes[0].Address != "mock_thing.a" {
		t.Fatalf("expected parent first, got %s", pl.Changes[0].Address)
	}
	for _, c := range pl.Changes {
		if c.Action != provider.ActionCreate {
			t.Fatalf("%s: expected create, got %s", c.Address, c.Action)
		}
	}
}

// TestBuildDeleteReverseDependencyOrder guards that deletes run in reverse
// dependency order using the recorded state dependencies, not lexical order.
// Here the dependent (mock_child.a) sorts lexically BEFORE its dependency
// (mock_thing.z), so a lexical-only ordering would delete the dependency first
// (wrong). Correct order deletes the dependent first.
func TestBuildDeleteReverseDependencyOrder(t *testing.T) {
	p := mock.New()
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "z", ID: "mock_thing/1"})
	s.Upsert(state.Resource{Type: "mock_child", Name: "a", ID: "mock_child/1",
		Dependencies: []string{"mock_thing.z"}})

	// Empty desired → everything is a delete.
	pl, err := Build(context.Background(), p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 2 {
		t.Fatalf("expected 2 deletes, got %d", len(pl.Changes))
	}
	for _, c := range pl.Changes {
		if c.Action != provider.ActionDelete {
			t.Fatalf("%s: expected delete, got %s", c.Address, c.Action)
		}
	}
	if pl.Changes[0].Address != "mock_child.a" || pl.Changes[1].Address != "mock_thing.z" {
		t.Fatalf("dependent must be deleted before its dependency; got order %s, %s",
			pl.Changes[0].Address, pl.Changes[1].Address)
	}
}

func TestBuildUpdateWhenAttrChanged(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("old")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})

	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("new")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 1 || pl.Changes[0].Action != provider.ActionUpdate {
		t.Fatalf("expected 1 update, got %+v", pl.Changes)
	}
	if len(pl.Changes[0].AttrDiffs) != 1 || pl.Changes[0].AttrDiffs[0].Name != "name" {
		t.Fatalf("expected a name diff, got %+v", pl.Changes[0].AttrDiffs)
	}
}

func TestBuildDriftRecreatesWhenRemoteMissing(t *testing.T) {
	p := mock.New() // provider store is EMPTY → Read returns ErrNotFound
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})

	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 1 || pl.Changes[0].Action != provider.ActionCreate {
		t.Fatalf("expected drift → recreate (ActionCreate), got %+v", pl.Changes)
	}
}

func TestUndeclaredActualAttrNotADiff(t *testing.T) {
	p := mock.New()
	// Remote has an extra attribute the config never declares.
	p.Seed("mock_thing/1", provider.Attributes{
		"name": cty.StringVal("x"), "extra": cty.StringVal("from-server")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})

	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("an attribute the config does not declare must not diff, got: %+v", pl.Changes)
	}
}

func TestComputedAttrNotADiff(t *testing.T) {
	p := mock.New()
	// Remote has a computed server_id the user never authored.
	p.Seed("mock_computed/1", provider.Attributes{
		"name": cty.StringVal("x"), "server_id": cty.StringVal("srv-1")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_computed", Name: "a", ID: "mock_computed/1"})

	desired := []DesiredResource{
		{Type: "mock_computed", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("computed-only difference must not be a change, got: %+v", pl.Changes)
	}
}

func TestResolveImportsPendingImportConverges(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("mock_thing/7", provider.Attributes{"name": cty.StringVal("adopted")})
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("adopted")}},
	}
	pl, err := Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	imports := []ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/7"}}
	if err := ResolveImports(ctx, p, s, pl, imports); err != nil {
		t.Fatal(err)
	}
	if !pl.HasChanges() {
		t.Fatal("a pending import is a change")
	}
	c := pl.Changes[0]
	if c.ImportID != "mock_thing/7" {
		t.Fatalf("expected ImportID set, got %q", c.ImportID)
	}
	if c.Action != provider.ActionNoop {
		t.Fatalf("config matches live → post-import reconcile should be Noop, got %v", c.Action)
	}
}

func TestResolveImportsShowsUpdateWhenConfigDiffers(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	p.Seed("mock_thing/7", provider.Attributes{"name": cty.StringVal("old")})
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("new")}},
	}
	pl, _ := Build(ctx, p, s, desired)
	if err := ResolveImports(ctx, p, s, pl, []ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/7"}}); err != nil {
		t.Fatal(err)
	}
	if pl.Changes[0].Action != provider.ActionUpdate {
		t.Fatalf("config differs from live → expected Update, got %v", pl.Changes[0].Action)
	}
}

func TestResolveImportsErrors(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	}

	// not declared
	pl, _ := Build(ctx, p, s, desired)
	if err := ResolveImports(ctx, p, s, pl, []ImportBlock{{Address: "mock_thing.ghost", ID: "id"}}); err == nil {
		t.Fatal("want a not-declared error")
	}
	// not found (empty store)
	pl2, _ := Build(ctx, p, s, desired)
	if err := ResolveImports(ctx, p, s, pl2, []ImportBlock{{Address: "mock_thing.a", ID: "missing"}}); err == nil {
		t.Fatal("want a not-found error")
	}
	// duplicate
	p.Seed("id1", provider.Attributes{"name": cty.StringVal("x")})
	pl3, _ := Build(ctx, p, s, desired)
	dup := []ImportBlock{{Address: "mock_thing.a", ID: "id1"}, {Address: "mock_thing.a", ID: "id1"}}
	if err := ResolveImports(ctx, p, s, pl3, dup); err == nil {
		t.Fatal("want a duplicate-import error")
	}
}

func TestResolveImportsUndeclaredErrorHasHint(t *testing.T) {
	p := mock.New()
	s := state.New()
	pl, err := Build(context.Background(), p, s, nil) // no declared resources
	if err != nil {
		t.Fatal(err)
	}
	err = ResolveImports(context.Background(), p, s, pl,
		[]ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/9"}})
	if err == nil || !strings.Contains(err.Error(), "--generate-config") {
		t.Fatalf("expected a hint about --generate-config, got %v", err)
	}
}

type sensitiveResource struct{}

func (sensitiveResource) Type() string                                    { return "fake_secret" }
func (sensitiveResource) DecodeSpec() hcldec.Spec                         { return hcldec.ObjectSpec{} }
func (sensitiveResource) Computed() []string                              { return []string{"value"} }
func (sensitiveResource) Sensitive() []string                             { return []string{"value"} }
func (sensitiveResource) Dependencies(provider.Attributes) []provider.Ref { return nil }
func (sensitiveResource) Create(context.Context, provider.Attributes) (string, provider.Attributes, error) {
	return "", nil, nil
}
func (sensitiveResource) Read(context.Context, string) (provider.Attributes, error) { return nil, nil }
func (sensitiveResource) Update(context.Context, string, provider.Attributes) (provider.Attributes, error) {
	return nil, nil
}
func (sensitiveResource) Delete(context.Context, string) error { return nil }

type sensitiveProvider struct{}

func (sensitiveProvider) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{"fake_secret": sensitiveResource{}}
}

func TestBuildPopulatesSensitive(t *testing.T) {
	pl, err := Build(context.Background(), sensitiveProvider{}, state.New(), []DesiredResource{
		{Type: "fake_secret", Name: "s", Attributes: provider.Attributes{"value": cty.StringVal("x")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 1 || len(pl.Changes[0].Sensitive) != 1 || pl.Changes[0].Sensitive[0] != "value" {
		t.Fatalf("Change.Sensitive not populated: %+v", pl.Changes)
	}
}

// SkipRefresh compares the config with state, without reading the resource:
// a change made outside Baseplate isn't seen, a config change is.
func TestBuildSkipRefresh(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("changed outside")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1",
		Attributes: encodeTestAttrs(t, provider.Attributes{"name": cty.StringVal("root")})})
	desired := func(name string) []DesiredResource {
		return []DesiredResource{{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal(name)}}}
	}
	ctx := context.Background()

	pl, err := BuildWithOptions(ctx, p, s, desired("root"), BuildOptions{SkipRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("without refreshing, the outside change must not show: %+v", pl.Changes)
	}
	if pl, _ := Build(ctx, p, s, desired("root")); !pl.HasChanges() {
		t.Fatal("a refreshing plan must see the outside change")
	}
	pl, err = BuildWithOptions(ctx, p, s, desired("renamed"), BuildOptions{SkipRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if !pl.HasChanges() || pl.Changes[0].Action != provider.ActionUpdate {
		t.Fatalf("a config change must show without refreshing: %+v", pl.Changes)
	}
}

func encodeTestAttrs(t *testing.T, attrs provider.Attributes) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for k, v := range attrs {
		b, err := ctyjson.Marshal(v, cty.DynamicPseudoType)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = b
	}
	return out
}
