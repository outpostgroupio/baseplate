package roblox

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// TestLiveAssetTextureID checks roblox_asset's texture_id output on one Image
// and one Decal: an Image's is its own id; a Decal's is a different asset, of
// type Image, resolved via toolbox-service (needs the creator-store-product:read
// scope). By default it uploads both (two uploads). Set
// BASEPLATE_TEST_IMAGE_ID and BASEPLATE_TEST_DECAL_ID to existing assets owned
// by the key's user to import them instead (no uploads; nothing is destroyed).
// SKIPPED unless BASEPLATE_LIVE=1 and ROBLOX_API_KEY are set.
//
//	set -a; . ./.env; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveAssetTextureID -v
func TestLiveAssetTextureID(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 (+ ROBLOX_API_KEY) to run the live texture_id test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	if key == "" {
		t.Fatal("ROBLOX_API_KEY must be set")
	}
	owner := os.Getenv("BASEPLATE_TEST_USER")
	if owner == "" {
		owner = "5822334358"
	}
	ownerID, err := strconv.ParseInt(owner, 10, 64)
	if err != nil {
		t.Fatalf("BASEPLATE_TEST_USER %q: %v", owner, err)
	}
	reuseImage, reuseDecal := os.Getenv("BASEPLATE_TEST_IMAGE_ID"), os.Getenv("BASEPLATE_TEST_DECAL_ID")
	reuse := reuseImage != "" && reuseDecal != ""

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, warns := provider.WithWarnings(context.Background())
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-texture.bpstate"))
	s := state.New()

	dir := t.TempDir()
	imgPath, decalPath := filepath.Join(dir, "image.png"), filepath.Join(dir, "decal.png")
	writeTestPNGColor(t, imgPath, color.RGBA{R: 0x22, G: 0xaa, B: 0x55, A: 0xff})
	writeTestPNGColor(t, decalPath, color.RGBA{R: 0xaa, G: 0x22, B: 0x99, A: 0xff})
	asset := func(name, file, typ string) plan.DesiredResource {
		return plan.DesiredResource{Type: "roblox_asset", Name: name, Attributes: provider.Attributes{
			"file":            cty.StringVal(file),
			"display_name":    cty.StringVal("Baseplate Live Texture " + typ),
			"description":     cty.StringVal("Created by Baseplate's live texture_id test"),
			"asset_type":      cty.StringVal(typ),
			"creator_user_id": cty.NumberIntVal(ownerID),
		}}
	}
	desired := []plan.DesiredResource{asset("img", imgPath, "Image"), asset("decal", decalPath, "Decal")}

	if reuse {
		for _, imp := range []struct{ address, id string }{{"roblox_asset.img", reuseImage}, {"roblox_asset.decal", reuseDecal}} {
			if err := apply.Import(ctx, p, b, s, imp.address, imp.id, desired); err != nil {
				t.Fatalf("import %s: %v", imp.address, err)
			}
		}
	}
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reuse {
		// Destroy unmanages the Image and archives the Decal (restorable).
		t.Cleanup(func() {
			dpl, err := plan.Build(context.Background(), p, s, nil)
			if err == nil {
				err = apply.Apply(context.Background(), p, b, s, dpl)
			}
			if err != nil {
				t.Errorf("destroy: %v", err)
			}
		})
	}
	t.Logf("warnings after the first apply: %q", warns.List())

	texture := func(address string) (id, tex string) {
		t.Helper()
		r, ok := s.Find(address)
		if !ok {
			t.Fatalf("%s not in state", address)
		}
		raw, ok := r.Attributes["texture_id"]
		if !ok {
			return r.ID, ""
		}
		v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType)
		if err != nil {
			t.Fatal(err)
		}
		return r.ID, v.AsString()
	}

	imgID, imgTex := texture("roblox_asset.img")
	if imgTex != imgID {
		t.Fatalf("Image texture_id = %q, want its id %s", imgTex, imgID)
	}
	decalID, decalTex := texture("roblox_asset.decal")
	t.Logf("image %s -> texture %s; decal %s -> texture %q", imgID, imgTex, decalID, decalTex)
	// toolbox-service 404s a new decal for a while after the upload (about a
	// minute, live): no-op applies must record texture_id once it resolves.
	for deadline := time.Now().Add(3 * time.Minute); decalTex == "" && time.Now().Before(deadline); {
		time.Sleep(15 * time.Second)
		pl2, err := plan.Build(ctx, p, s, desired)
		if err != nil {
			t.Fatalf("re-plan: %v", err)
		}
		if pl2.HasChanges() {
			t.Fatalf("a missing texture_id must not be a change:\n%s", pl2.RenderText(false))
		}
		if err := apply.Apply(ctx, p, b, s, pl2); err != nil {
			t.Fatalf("re-apply: %v", err)
		}
		_, decalTex = texture("roblox_asset.decal")
		t.Logf("decal texture_id after a no-op apply: %q", decalTex)
	}
	if decalTex == "" || decalTex == decalID {
		t.Fatalf("Decal texture_id = %q, want an asset id other than the decal's (%s); warnings %q", decalTex, decalID, warns.List())
	}
	got, err := opencloud.NewAssetClient(opencloud.New(key, assetsBase)).Get(context.Background(), decalTex)
	if err != nil {
		t.Fatalf("reading the texture asset %s: %v", decalTex, err)
	}
	if got.AssetType != "Image" {
		t.Fatalf("texture asset %s has type %q, want Image", decalTex, got.AssetType)
	}

	// Read reports the same value (one toolbox call for the Decal).
	read, err := p.Resources()["roblox_asset"].Read(ctx, decalID)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := read["texture_id"]; !ok || v.AsString() != decalTex {
		t.Fatalf("Read texture_id = %#v, want %s", v, decalTex)
	}

	// Convergence: texture_id never diffs.
	pl3, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("converge plan: %v", err)
	}
	if pl3.HasChanges() {
		t.Fatalf("expected convergence, got:\n%s", pl3.RenderText(false))
	}
	t.Logf("live texture_id OK: Image %s -> %s; Decal %s -> Image %s (warnings %q)", imgID, imgTex, decalID, decalTex, warns.List())
}
