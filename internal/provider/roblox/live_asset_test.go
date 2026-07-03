package roblox

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/engine/apply"
	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/state"
	"github.com/outpostgroup/baseplate/internal/state/backend"
)

// writeTestPNG encodes a small opaque 64x64 PNG to path. Generated in-process so
// no binary fixture lives in the repo, and large enough to be a valid image upload.
func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 0x33, G: 0x88, B: 0xff, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLiveAssetLifecycle runs the full plan→apply→converge→archive lifecycle for
// roblox_asset against real Roblox Open Cloud: multipart upload → operation poll →
// read back → convergence → archive (soft delete, self-cleaning). SKIPPED unless
// BASEPLATE_LIVE=1 and ROBLOX_API_KEY are set, so it never runs in CI.
//
// Run it with:
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run Live -v
func TestLiveAssetLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 (+ ROBLOX_API_KEY) to run the live asset test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	if key == "" {
		t.Fatal("ROBLOX_API_KEY must be set")
	}
	// Assets are owned by the API key's account. Defaults to the documented test
	// user (users/5822334358); override with BASEPLATE_TEST_USER if the key differs.
	owner := os.Getenv("BASEPLATE_TEST_USER")
	if owner == "" {
		owner = "5822334358"
	}
	ownerID, err := strconv.ParseInt(owner, 10, 64)
	if err != nil {
		t.Fatalf("BASEPLATE_TEST_USER %q: %v", owner, err)
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-asset.tfstate"))
	s := state.New()

	pngPath := filepath.Join(t.TempDir(), "baseplate-live.png")
	writeTestPNG(t, pngPath)

	desired := []plan.DesiredResource{
		{Type: "roblox_asset", Name: "icon", Attributes: provider.Attributes{
			"file":            cty.StringVal(pngPath),
			"display_name":    cty.StringVal("Baseplate Live Test Icon"),
			"description":     cty.StringVal("Created by Baseplate's live asset test"),
			"asset_type":      cty.StringVal("Image"),
			"creator_user_id": cty.NumberIntVal(ownerID),
		}},
	}

	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	t.Logf("initial plan:\n%s", pl.RenderText(false))
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Self-clean: archive the uploaded asset regardless of what follows.
	t.Cleanup(func() {
		dpl, err := plan.Build(ctx, p, s, nil)
		if err != nil {
			t.Errorf("destroy plan: %v", err)
			return
		}
		if err := apply.Apply(ctx, p, b, s, dpl); err != nil {
			t.Errorf("destroy (archive): %v", err)
		}
	})

	// Convergence: a second plan against the resulting state must be a no-op.
	pl2, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if pl2.HasChanges() {
		t.Fatalf("expected convergence, got changes:\n%s", pl2.RenderText(false))
	}

	t.Log("live asset lifecycle OK: uploaded via multipart+operation-poll, read back, converged, archived")
}
