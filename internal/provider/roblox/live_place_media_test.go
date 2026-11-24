package roblox

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// TestLivePlaceMedia sets the test place's icon and thumbnails, reorders them
// and removes the icon, re-imports, and checks that each plan converges. The
// place's original icon and thumbnails are restored afterwards. By default it
// uploads three images (Image assets can't be archived, so they're left
// behind); set BASEPLATE_TEST_MEDIA_IMAGES to three comma-separated image ids
// owned by the key's user to reuse them. SKIPPED unless BASEPLATE_LIVE=1 and
// ROBLOX_API_KEY + BASEPLATE_TEST_PLACE + BASEPLATE_TEST_USER are set. The key
// needs asset:read and asset:write.
//
//	set -a; . ./.env; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LivePlaceMedia -v
func TestLivePlaceMedia(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live place media test")
	}
	key, placeID, owner := os.Getenv("ROBLOX_API_KEY"), os.Getenv("BASEPLATE_TEST_PLACE"), os.Getenv("BASEPLATE_TEST_USER")
	if key == "" || placeID == "" || owner == "" {
		t.Skip("set ROBLOX_API_KEY, BASEPLATE_TEST_PLACE and BASEPLATE_TEST_USER")
	}
	ctx := context.Background()
	assets := opencloud.New(key, assetsBase)
	mc := opencloud.NewPlaceMediaClient(assets)

	orig, err := mc.Get(ctx, placeID)
	if err != nil {
		t.Fatalf("read place media: %v", err)
	}
	t.Cleanup(func() {
		if err := mc.Update(ctx, placeID, orig, []string{"icon", "previews"}); err != nil {
			t.Errorf("restore place media: %v", err)
		}
	})

	imgs := liveMediaImages(t, opencloud.NewAssetClient(assets), owner)
	thumbs := func(pairs ...string) cty.Value {
		var elems []cty.Value
		for i := 0; i < len(pairs); i += 2 {
			o := map[string]cty.Value{"asset_id": cty.StringVal(pairs[i])}
			if pairs[i+1] != "" {
				o["alt_text"] = cty.StringVal(pairs[i+1])
			}
			elems = append(elems, cty.ObjectVal(o))
		}
		v, err := convert.Convert(cty.TupleVal(elems), cty.List(cty.ObjectWithOptionalAttrs(map[string]cty.Type{
			"asset_id": cty.String, "alt_text": cty.String,
		}, []string{"alt_text"})))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	media := func(icon string, thumbnails cty.Value) []plan.DesiredResource {
		return []plan.DesiredResource{{Type: "roblox_place_media", Name: "root", Attributes: provider.Attributes{
			"place_id":   cty.StringVal(placeID),
			"icon":       cty.StringVal(icon),
			"thumbnails": thumbnails,
		}}}
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-place-media.bpstate"))
	s := state.New()
	// Reads lag behind writes: for a minute or two after a PATCH, about one
	// read in ten still returns the previous icon and thumbnails. So
	// convergence is "a plan with no changes within three minutes", not "the
	// next plan has no changes".
	converge := func(step string, desired []plan.DesiredResource) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for {
			pl, err := plan.Build(ctx, p, s, desired)
			if err != nil {
				t.Fatalf("%s: re-plan: %v", step, err)
			}
			if !pl.HasChanges() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: expected convergence, got:\n%s", step, pl.RenderText(false))
			}
			t.Logf("%s: re-plan still shows changes (stale read), retrying", step)
			time.Sleep(5 * time.Second)
		}
	}
	applyAndConverge := func(step string, desired []plan.DesiredResource) {
		t.Helper()
		pl, err := plan.Build(ctx, p, s, desired)
		if err != nil {
			t.Fatalf("%s: plan: %v", step, err)
		}
		t.Logf("%s plan:\n%s", step, pl.RenderText(false))
		if err := apply.Apply(ctx, p, b, s, pl); err != nil {
			t.Fatalf("%s: apply: %v", step, err)
		}
		converge(step, desired)
	}
	// check reads Roblox directly until it reports the expected values (see
	// converge for why one read isn't enough).
	check := func(step, icon string, want ...opencloud.PlaceThumbnail) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for {
			got, err := mc.Get(ctx, placeID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Icon == icon && fmt.Sprint(got.Thumbnails) == fmt.Sprint(want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: Roblox has icon %q, thumbnails %v; want %q, %v", step, got.Icon, got.Thumbnails, icon, want)
			}
			time.Sleep(2 * time.Second)
		}
	}

	set := media(imgs[0], thumbs(imgs[1], "Baseplate live test", imgs[2], ""))
	applyAndConverge("set", set)
	check("set", imgs[0], opencloud.PlaceThumbnail{AssetID: imgs[1], AltText: "Baseplate live test"}, opencloud.PlaceThumbnail{AssetID: imgs[2]})

	reorder := media("", thumbs(imgs[2], "", imgs[1], ""))
	applyAndConverge("reorder and remove icon", reorder)
	check("reorder and remove icon", "", opencloud.PlaceThumbnail{AssetID: imgs[2]}, opencloud.PlaceThumbnail{AssetID: imgs[1]})

	// A fresh state that imports the place sees no changes.
	s = state.New()
	if err := apply.Import(ctx, p, b, s, "roblox_place_media.root", placeID, reorder); err != nil {
		t.Fatalf("import: %v", err)
	}
	converge("import", reorder)

	applyAndConverge("clear thumbnails", media("", thumbs()))
	check("clear thumbnails", "")
}

// liveMediaImages returns three image ids: BASEPLATE_TEST_MEDIA_IMAGES, or
// three new uploads owned by the test user.
func liveMediaImages(t *testing.T, ac opencloud.AssetClient, owner string) []string {
	t.Helper()
	if v := os.Getenv("BASEPLATE_TEST_MEDIA_IMAGES"); v != "" {
		ids := strings.Split(v, ",")
		if len(ids) != 3 {
			t.Fatalf("BASEPLATE_TEST_MEDIA_IMAGES must list three image ids, got %q", v)
		}
		return ids
	}
	ownerID, err := strconv.ParseInt(owner, 10, 64)
	if err != nil {
		t.Fatalf("BASEPLATE_TEST_USER %q: %v", owner, err)
	}
	dir := t.TempDir()
	var ids []string
	for i, c := range []color.RGBA{{R: 0x33, G: 0x66, B: 0xcc, A: 0xff}, {R: 0xcc, G: 0x66, B: 0x33, A: 0xff}, {R: 0x33, G: 0xaa, B: 0x55, A: 0xff}} {
		path := filepath.Join(dir, fmt.Sprintf("media%d.png", i))
		writeTestPNGColor(t, path, c)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		a, err := ac.CreateAndAwait(context.Background(), opencloud.AssetCreate{
			AssetType: "Image", DisplayName: fmt.Sprintf("Baseplate Live Media %d", i),
			Description: "Created by Baseplate's live place media test", CreatorUserID: ownerID,
		}, filepath.Base(path), "image/png", data)
		if err != nil {
			t.Fatalf("upload image %d: %v", i, err)
		}
		ids = append(ids, a.ID)
	}
	t.Logf("uploaded images %v (set BASEPLATE_TEST_MEDIA_IMAGES=%s to reuse them)", ids, strings.Join(ids, ","))
	return ids
}
