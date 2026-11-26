package apply

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// prProvider has pr_item, replaced (a new id) when its name changes, and
// pr_list, whose items are a list of { id, label } that can reference items.
type prProvider struct {
	next  int
	store map[string]provider.Attributes
	got   cty.Value // the items pr_list was last created/updated with
}

var prItemType = cty.Object(map[string]cty.Type{"id": cty.String, "label": cty.String})

func (p *prProvider) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{"pr_item": &prItem{p}, "pr_list": &prList{p}}
}

func (p *prProvider) put(d provider.Attributes) string {
	p.next++
	id := fmt.Sprint(p.next)
	p.store[id] = d
	return id
}

func (p *prProvider) read(id string) (provider.Attributes, error) {
	a, ok := p.store[id]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return a, nil
}

type prItem struct{ p *prProvider }

func (r *prItem) Type() string { return "pr_item" }
func (r *prItem) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{"name": &hcldec.AttrSpec{Name: "name", Type: cty.String, Required: true}}
}
func (r *prItem) Computed() []string                              { return []string{"id"} }
func (r *prItem) Dependencies(provider.Attributes) []provider.Ref { return nil }
func (r *prItem) Read(_ context.Context, id string) (provider.Attributes, error) {
	return r.p.read(id)
}
func (r *prItem) Create(_ context.Context, d provider.Attributes) (string, provider.Attributes, error) {
	return r.p.put(d), d, nil
}
func (r *prItem) Update(_ context.Context, id string, d provider.Attributes) (provider.Attributes, error) {
	r.p.store[id] = d
	return d, nil
}
func (r *prItem) Delete(_ context.Context, id string) error { delete(r.p.store, id); return nil }
func (r *prItem) RequiresReplace(desired, actual provider.Attributes) []string {
	if !desired["name"].RawEquals(actual["name"]) {
		return []string{"name"}
	}
	return nil
}
func (r *prItem) ReplaceAbandons() bool { return false }

type prList struct{ p *prProvider }

func (r *prList) Type() string { return "pr_list" }
func (r *prList) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{"items": &hcldec.AttrSpec{Name: "items", Type: cty.List(prItemType)}}
}
func (r *prList) Computed() []string                              { return []string{"id"} }
func (r *prList) Dependencies(provider.Attributes) []provider.Ref { return nil }
func (r *prList) Read(_ context.Context, id string) (provider.Attributes, error) {
	return r.p.read(id)
}
func (r *prList) Create(_ context.Context, d provider.Attributes) (string, provider.Attributes, error) {
	r.p.got = d["items"]
	return r.p.put(d), d, nil
}
func (r *prList) Update(_ context.Context, id string, d provider.Attributes) (provider.Attributes, error) {
	r.p.got = d["items"]
	r.p.store[id] = d
	return d, nil
}
func (r *prList) Delete(_ context.Context, id string) error { delete(r.p.store, id); return nil }

// prDesired is pr_item.a named name, and pr_list.l whose second item's id is
// a reference to pr_item.a.id, as config loads it: an unknown at that path.
func prDesired(name string) []plan.DesiredResource {
	items := cty.ListVal([]cty.Value{
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("lit"), "label": cty.StringVal("x")}),
		cty.ObjectVal(map[string]cty.Value{"id": cty.UnknownVal(cty.String), "label": cty.StringVal("y")}),
	})
	return []plan.DesiredResource{
		{Type: "pr_item", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal(name)}},
		{
			Type: "pr_list", Name: "l", DependsOn: []string{"pr_item.a"},
			Attributes: provider.Attributes{"items": items},
			PathRefs: map[string][]plan.PathRef{"items": {{
				Path: cty.Path{cty.IndexStep{Key: cty.NumberIntVal(1)}, cty.GetAttrStep{Name: "id"}},
				Ref:  provider.Ref{Address: "pr_item.a", Attr: "id"},
			}}},
		},
	}
}

func prPlan(t *testing.T, p *prProvider, s *state.State, desired []plan.DesiredResource) *plan.Plan {
	t.Helper()
	pl, err := plan.Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

func wantItems(t *testing.T, got cty.Value, id string) {
	t.Helper()
	want := cty.ListVal([]cty.Value{
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("lit"), "label": cty.StringVal("x")}),
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal(id), "label": cty.StringVal("y")}),
	})
	if !got.RawEquals(want) {
		t.Fatalf("items = %#v, want the second id %s", got, id)
	}
}

// A reference inside a list: plan shows only that field as known after
// apply, apply fills in the referenced id, and the next plan converges. When
// the referenced resource is replaced, the list is updated with the new id.
func TestApplyReferenceInsideList(t *testing.T) {
	ctx := context.Background()
	p := &prProvider{store: map[string]provider.Attributes{}}
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))

	pl := prPlan(t, p, s, prDesired("one"))
	text := pl.RenderText(false)
	for _, want := range []string{`{"id":"lit","label":"x"}`, `{"id":(known after apply),"label":"y"}`} {
		if !strings.Contains(text, want) {
			t.Fatalf("create plan missing %s:\n%s", want, text)
		}
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	item, _ := s.Find("pr_item.a")
	oldID := item.ID
	wantItems(t, p.got, oldID)
	if pl := prPlan(t, p, s, prDesired("one")); pl.HasChanges() {
		t.Fatalf("re-plan after apply has changes:\n%s", pl.RenderText(false))
	}

	pl = prPlan(t, p, s, prDesired("two"))
	var listChange *plan.Change
	for i := range pl.Changes {
		if pl.Changes[i].Address == "pr_list.l" {
			listChange = &pl.Changes[i]
		}
	}
	if listChange == nil || listChange.Action != provider.ActionUpdate {
		t.Fatalf("replacing pr_item.a should update pr_list.l:\n%s", pl.RenderText(false))
	}
	if text := pl.RenderText(false); !strings.Contains(text, `{"id":(known after apply),"label":"y"}`) {
		t.Fatalf("update plan should show the new id as unknown:\n%s", text)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	newItem, _ := s.Find("pr_item.a")
	if newItem.ID == oldID {
		t.Fatal("pr_item.a was not replaced")
	}
	wantItems(t, p.got, newItem.ID)
	if pl := prPlan(t, p, s, prDesired("two")); pl.HasChanges() {
		t.Fatalf("re-plan after replace has changes:\n%s", pl.RenderText(false))
	}
}

// Drift on a literal element is still detected while the reference resolves
// from state, and the update sends the resolved id.
func TestApplyReferenceInsideListDrift(t *testing.T) {
	ctx := context.Background()
	p := &prProvider{store: map[string]provider.Attributes{}}
	s := state.New()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))
	if err := Apply(ctx, p, b, s, prPlan(t, p, s, prDesired("one"))); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Find("pr_list.l")
	p.store[list.ID] = provider.Attributes{"items": cty.ListValEmpty(prItemType)}
	pl := prPlan(t, p, s, prDesired("one"))
	if len(pl.Changes) == 0 || pl.Count() != 1 {
		t.Fatalf("want one update for the drift:\n%s", pl.RenderText(false))
	}
	if text := pl.RenderText(false); strings.Contains(text, "known after apply") {
		t.Fatalf("the reference resolves from state, so nothing is unknown:\n%s", text)
	}
	if err := Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	item, _ := s.Find("pr_item.a")
	wantItems(t, p.got, item.ID)
}
