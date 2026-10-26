package resources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

type assetOnly struct{ r provider.Resource }

func (p assetOnly) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{"roblox_asset": p.r}
}

type assetHarness struct {
	t   *testing.T
	ctx context.Context
	f   *fakeAsset
	p   assetOnly
	s   *state.State
	b   backend.Backend
}

func newAssetHarness(t *testing.T) *assetHarness {
	f := &fakeAsset{}
	return &assetHarness{t: t, ctx: context.Background(), f: f, p: assetOnly{Asset(f)}, s: state.New(),
		b: backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))}
}

func assetDesired(file string) []plan.DesiredResource {
	return []plan.DesiredResource{{Type: "roblox_asset", Name: "x", Attributes: provider.Attributes{
		"file": cty.StringVal(file), "display_name": cty.StringVal("X"),
		"description": cty.StringVal("x"), "creator_user_id": cty.NumberIntVal(1),
	}}}
}

func (h *assetHarness) plan(d []plan.DesiredResource) *plan.Plan {
	h.t.Helper()
	pl, err := plan.Build(h.ctx, h.p, h.s, d)
	if err != nil {
		h.t.Fatal(err)
	}
	return pl
}

func (h *assetHarness) apply(pl *plan.Plan) {
	h.t.Helper()
	if err := apply.Apply(h.ctx, h.p, h.b, h.s, pl); err != nil {
		h.t.Fatal(err)
	}
}

// Changing the file's extension with asset_type omitted changes the inferred
// type, which Roblox can't do in place: plan must replace (both directions),
// never PATCH the existing asset with another type.
func TestAssetInferredTypeChangeReplaces(t *testing.T) {
	for _, tc := range []struct{ from, to string }{{"a.png", "a.fbx"}, {"a.fbx", "a.png"}} {
		t.Run(tc.from+"->"+tc.to, func(t *testing.T) {
			h := newAssetHarness(t)
			from := writeFile(t, tc.from, "v1")
			h.apply(h.plan(assetDesired(from)))
			to := writeFile(t, tc.to, "v2")
			pl := h.plan(assetDesired(to))
			c := pl.Changes[0]
			if c.Action != provider.ActionReplace || !strings.Contains(strings.Join(c.Replace, ","), "asset_type") {
				t.Fatalf("want a replace forced by asset_type, got %s %v\n%s", c.Action, c.Replace, pl.RenderText(false))
			}
			h.apply(pl)
			if h.f.updates != 0 || len(h.f.content) != 0 {
				t.Fatal("a type change must never PATCH the existing asset")
			}
			if again := h.plan(assetDesired(to)); again.HasChanges() {
				t.Fatalf("must converge:\n%s", again.RenderText(false))
			}
		})
	}
}

// Importing an asset whose remote type differs from the (inferred) config
// type plans "imported, then replaced", and leaves the adopted asset alone.
func TestAssetImportTypeMismatchReplaces(t *testing.T) {
	h := newAssetHarness(t)
	h.f.store = map[string]opencloud.Asset{"777": {ID: "777", AssetType: "Decal", DisplayName: "X", Description: "x"}}
	fp := writeFile(t, "a.png", "png")
	pl := h.plan(assetDesired(fp))
	if err := plan.ResolveImports(h.ctx, h.p, h.s, pl, []plan.ImportBlock{{Address: "roblox_asset.x", ID: "777"}}); err != nil {
		t.Fatal(err)
	}
	c := pl.Changes[0]
	if c.Action != provider.ActionReplace || c.ImportID != "777" || !c.AbandonsOld {
		t.Fatalf("want import then abandoning replace, got %s import=%q abandons=%v", c.Action, c.ImportID, c.AbandonsOld)
	}
	if out := pl.RenderText(false); !strings.Contains(out, "asset_type: Decal => Image  # forces replacement") ||
		!strings.Contains(out, "left in place") {
		t.Fatalf("render:\n%s", out)
	}
	h.apply(pl)
	if _, ok := h.f.store["777"]; !ok {
		t.Fatal("the adopted asset must be left in place")
	}
}

// A plan on a machine without the source file (e.g. CI) must not fail for an
// asset already in state: no change, and a warning.
func TestAssetPlanWithMissingFileWarns(t *testing.T) {
	h := newAssetHarness(t)
	fp := writeFile(t, "a.png", "png")
	h.apply(h.plan(assetDesired(fp)))
	missing := filepath.Join(t.TempDir(), "a.png")
	pl := h.plan(assetDesired(missing))
	if pl.HasChanges() {
		t.Fatalf("missing file must not plan a change:\n%s", pl.RenderText(false))
	}
	if w := strings.Join(pl.Warnings(), "\n"); !strings.Contains(w, "roblox_asset.x: file \""+missing+"\" not found; content changes can't be detected") {
		t.Fatalf("warning = %q", w)
	}
}

// State from before content_hash existed has no hash. For an Image (content
// updates in place, so the trigger doesn't force a replace) that must still
// not plan a re-upload of every asset: the current hash is adopted.
func TestAssetMissingHashBaselineDoesNotReupload(t *testing.T) {
	h := newAssetHarness(t)
	fp := writeFile(t, "a.png", "png")
	h.apply(h.plan(assetDesired(fp)))
	st, _ := h.s.Find("roblox_asset.x")
	st.Triggers = nil
	pl := h.plan(assetDesired(fp))
	if pl.HasChanges() {
		t.Fatalf("a missing baseline must not plan an update:\n%s", pl.RenderText(false))
	}
	h.apply(pl)
	if len(h.f.content) != 0 {
		t.Fatal("nothing may be uploaded")
	}
	if st, _ := h.s.Find("roblox_asset.x"); st.Triggers["content_hash"] == nil {
		t.Fatal("apply must record the baseline")
	}
}

// An Image content change keeps the id (in-place update), and converges.
func TestAssetImageContentChangeKeepsID(t *testing.T) {
	h := newAssetHarness(t)
	fp := writeFile(t, "a.png", "v1")
	h.apply(h.plan(assetDesired(fp)))
	before, _ := h.s.Find("roblox_asset.x")
	id := before.ID
	if err := os.WriteFile(fp, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	pl := h.plan(assetDesired(fp))
	if c := pl.Changes[0]; c.Action != provider.ActionUpdate {
		t.Fatalf("an Image content change must be an in-place update, got %s", c.Action)
	}
	h.apply(pl)
	if after, _ := h.s.Find("roblox_asset.x"); after.ID != id || string(h.f.content[id]) != "v2" {
		t.Fatalf("id %s -> %s, content %q", id, after.ID, h.f.content[id])
	}
	if again := h.plan(assetDesired(fp)); again.HasChanges() {
		t.Fatalf("must converge:\n%s", again.RenderText(false))
	}
}

// Same type, same content: an imported asset converges with no replace.
func TestAssetImportMatchingTypeDoesNotReplace(t *testing.T) {
	h := newAssetHarness(t)
	h.f.store = map[string]opencloud.Asset{"777": {ID: "777", AssetType: "Image", DisplayName: "X", Description: "x"}}
	fp := writeFile(t, "a.png", "png")
	pl := h.plan(assetDesired(fp))
	if err := plan.ResolveImports(h.ctx, h.p, h.s, pl, []plan.ImportBlock{{Address: "roblox_asset.x", ID: "777"}}); err != nil {
		t.Fatal(err)
	}
	if c := pl.Changes[0]; c.Action != provider.ActionNoop {
		t.Fatalf("want a plain import, got %s", c.Action)
	}
}
