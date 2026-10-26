package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

func createAsset(t *testing.T, ctx context.Context, f *fakeAsset, file, typ string) (string, provider.Attributes) {
	t.Helper()
	d := provider.Attributes{
		"file": cty.StringVal(writeFile(t, file, "data")), "display_name": cty.StringVal("x"),
		"description": cty.StringVal("x"), "creator_user_id": cty.NumberIntVal(1),
	}
	if typ != "" {
		d["asset_type"] = cty.StringVal(typ)
	}
	id, out, err := Asset(f, f).Create(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	return id, out
}

func textureOf(a provider.Attributes) (string, bool) {
	v, ok := a["texture_id"]
	if !ok {
		return "", false
	}
	return v.AsString(), true
}

func TestAssetTextureIDImageIsItsID(t *testing.T) {
	f := &fakeAsset{}
	id, out := createAsset(t, context.Background(), f, "a.png", "")
	if got, ok := textureOf(out); !ok || got != id {
		t.Fatalf("Create texture_id = %q (%v), want the asset id %s", got, ok, id)
	}
	read, err := Asset(f, f).Read(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := textureOf(read); !ok || got != id {
		t.Fatalf("Read texture_id = %q (%v), want %s", got, ok, id)
	}
	if f.textureCalls != 0 {
		t.Fatalf("an Image must not cost a texture lookup, got %d", f.textureCalls)
	}
}

func TestAssetTextureIDDecalLookedUp(t *testing.T) {
	f := &fakeAsset{}
	r := Asset(f, f)
	id, out := createAsset(t, context.Background(), f, "a.png", "Decal")
	if got, ok := textureOf(out); !ok || got != "tex-"+id {
		t.Fatalf("Create texture_id = %q (%v), want tex-%s", got, ok, id)
	}
	read, err := r.Read(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := textureOf(read); got != "tex-"+id {
		t.Fatalf("Read texture_id = %q", got)
	}
	upd, err := r.Update(context.Background(), id, provider.Attributes{
		"file": cty.StringVal(writeFile(t, "a.png", "data")), "display_name": cty.StringVal("renamed"),
		"description": cty.StringVal("x"), "asset_type": cty.StringVal("Decal"), "creator_user_id": cty.NumberIntVal(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := textureOf(upd); got != "tex-"+id {
		t.Fatalf("Update texture_id = %q", got)
	}
	if f.textureCalls != 3 {
		t.Fatalf("want one lookup per Create/Read/Update of a Decal, got %d", f.textureCalls)
	}
}

func TestAssetTextureIDUnsetForOtherTypes(t *testing.T) {
	for _, tc := range []struct{ file, typ string }{{"a.fbx", ""}, {"a.ogg", ""}, {"a.png", "Model"}} {
		f := &fakeAsset{}
		id, out := createAsset(t, context.Background(), f, tc.file, tc.typ)
		if _, ok := textureOf(out); ok {
			t.Fatalf("%s/%s: Create must leave texture_id unset, got %v", tc.file, tc.typ, out["texture_id"])
		}
		read, err := Asset(f, f).Read(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := textureOf(read); ok {
			t.Fatalf("%s/%s: Read must leave texture_id unset", tc.file, tc.typ)
		}
		if f.textureCalls != 0 {
			t.Fatalf("%s/%s: no lookup expected, got %d", tc.file, tc.typ, f.textureCalls)
		}
	}
}

// A failed Decal lookup never fails the operation: texture_id is left unset
// and the reason is reported as a warning.
func TestAssetTextureIDLookupFailureWarns(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"not found", &opencloud.APIError{Status: 404}, "404"},
		{"server error", &opencloud.APIError{Status: 500, Message: "boom"}, "boom"},
		{"forbidden, not scope", &opencloud.APIError{Status: 403, Message: "You are not authorized."}, "not authorized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, w := provider.WithWarnings(context.Background())
			f := &fakeAsset{textureErr: tc.err}
			id, out := createAsset(t, ctx, f, "a.png", "Decal")
			if _, ok := textureOf(out); ok {
				t.Fatal("texture_id must be unset when the lookup fails")
			}
			read, err := Asset(f, f).Read(ctx, id)
			if err != nil {
				t.Fatalf("a failed lookup must not fail Read: %v", err)
			}
			if _, ok := textureOf(read); ok {
				t.Fatal("texture_id must be unset when the lookup fails")
			}
			ws := w.List()
			if len(ws) != 1 || !strings.Contains(ws[0], id) || !strings.Contains(ws[0], tc.want) {
				t.Fatalf("want one warning naming decal %s and %q, got %q", id, tc.want, ws)
			}
		})
	}
}

// A missing scope is reported once, and the remaining Decals skip the lookup.
func TestAssetTextureIDMissingScopeWarnsOnce(t *testing.T) {
	ctx, w := provider.WithWarnings(context.Background())
	f := &fakeAsset{textureErr: &opencloud.APIError{Status: 403, Message: "Scope not authorized."}}
	r := Asset(f, f)
	// createAsset builds a new resource each time (one lookup each); Reads
	// through one resource share the "scope missing" memo.
	for i := 0; i < 3; i++ {
		createAsset(t, ctx, f, "a.png", "Decal")
	}
	for _, id := range []string{"555", "556", "557"} {
		if _, err := r.Read(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	ws := w.List()
	if len(ws) != 1 || !strings.Contains(ws[0], "creator-store-product:read") {
		t.Fatalf("want one warning naming the scope, got %q", ws)
	}
	if f.textureCalls != 4 { // 3 creates (separate resources) + 1 read
		t.Fatalf("texture lookups = %d, want 4 (later Reads skip after the scope error)", f.textureCalls)
	}
}

// Through the engine: texture_id never diffs, a Decal whose lookup failed at
// create gets texture_id on a later no-op apply, and a reference resolves.
func TestAssetTextureIDEngine(t *testing.T) {
	h := newAssetHarness(t)
	h.f.textureErr = &opencloud.APIError{Status: 404}
	d := assetDesired(writeFile(t, "a.png", "v1"))
	d[0].Attributes["asset_type"] = cty.StringVal("Decal")
	h.apply(h.plan(d))
	st, _ := h.s.Find("roblox_asset.x")
	if _, ok := st.Attributes["texture_id"]; ok {
		t.Fatal("texture_id must be unset after a failed lookup")
	}
	h.f.textureErr = nil
	pl := h.plan(d)
	if pl.HasChanges() {
		t.Fatalf("texture_id must never be a change:\n%s", pl.RenderText(false))
	}
	h.apply(pl)
	st, _ = h.s.Find("roblox_asset.x")
	v, err := ctyjson.Unmarshal(st.Attributes["texture_id"], cty.DynamicPseudoType)
	if err != nil || v.AsString() != "tex-"+st.ID {
		t.Fatalf("texture_id in state = %#v (%v), want tex-%s", v, err, st.ID)
	}
	// A later failed lookup keeps the recorded value.
	h.f.textureErr = &opencloud.APIError{Status: 500}
	h.apply(h.plan(d))
	st, _ = h.s.Find("roblox_asset.x")
	if _, ok := st.Attributes["texture_id"]; !ok {
		t.Fatal("a failed lookup must keep the recorded texture_id")
	}
}

func TestAssetTextureIDImport(t *testing.T) {
	h := newAssetHarness(t)
	d := assetDesired(writeFile(t, "a.png", "v1"))
	h.apply(h.plan(d))
	st, _ := h.s.Find("roblox_asset.x")
	h.f.store["900"] = opencloud.Asset{ID: "900", DisplayName: "X", Description: "x", AssetType: "Decal"}
	imp := append(d, plan.DesiredResource{Type: "roblox_asset", Name: "y", Attributes: d[0].Attributes})
	if err := apply.Import(h.ctx, h.p, h.b, h.s, "roblox_asset.y", "900", imp); err != nil {
		t.Fatal(err)
	}
	y, _ := h.s.Find("roblox_asset.y")
	v, _ := ctyjson.Unmarshal(y.Attributes["texture_id"], cty.DynamicPseudoType)
	if v.AsString() != "tex-900" {
		t.Fatalf("imported Decal texture_id = %#v, want tex-900", v)
	}
	x, _ := ctyjson.Unmarshal(st.Attributes["texture_id"], cty.DynamicPseudoType)
	if x.AsString() != st.ID {
		t.Fatalf("Image texture_id = %#v, want its id %s", x, st.ID)
	}
}
