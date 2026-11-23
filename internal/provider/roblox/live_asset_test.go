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
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// writeTestPNG encodes a small opaque 64x64 PNG to path. Generated in-process so
// no binary fixture lives in the repo, and large enough to be a valid image upload.
func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	writeTestPNGColor(t, path, color.RGBA{R: 0x33, G: 0x88, B: 0xff, A: 0xff})
}

// writeTestPNGColor is writeTestPNG with a chosen fill, so a test can change
// the file's content.
func writeTestPNGColor(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
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
//	set -a; . ./.env; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run Live -v
func TestLiveAssetLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 (+ ROBLOX_API_KEY) to run the live asset test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	if key == "" {
		t.Fatal("ROBLOX_API_KEY must be set")
	}
	// Assets are owned by the API key's account.
	owner := os.Getenv("BASEPLATE_TEST_USER")
	if owner == "" {
		t.Fatal("BASEPLATE_TEST_USER must be set to the user id that owns the API key")
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
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-asset.bpstate"))
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

	// Metadata update: rename + re-describe, which goes through the multipart
	// PATCH (updateMask + Operation), then converge again.
	desired[0].Attributes["display_name"] = cty.StringVal("Baseplate Live Test Icon (renamed)")
	desired[0].Attributes["description"] = cty.StringVal("Updated by Baseplate's live asset test")
	pl3, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("update plan: %v", err)
	}
	t.Logf("update plan:\n%s", pl3.RenderText(false))
	if err := apply.Apply(ctx, p, b, s, pl3); err != nil {
		t.Fatalf("update apply: %v", err)
	}
	pl4, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("post-update re-plan: %v", err)
	}
	if pl4.HasChanges() {
		t.Fatalf("expected convergence after update, got changes:\n%s", pl4.RenderText(false))
	}

	// Content change: a new PNG under the same path. Images take new content
	// in place, so plan must call for an update (no replace) and apply must
	// keep the id. No new asset is uploaded, only a new version.
	oldRes, _ := s.Find("roblox_asset.icon")
	oldID := oldRes.ID
	// Roblox quirk (live, 2026-10-26): a content update sent within a few
	// seconds of a separate metadata update can end up with the earlier
	// name/description (the next plan then shows a metadata update that fixes
	// it). Real applies send both in one PATCH; leave a gap here so the test
	// checks the content path, not that race.
	time.Sleep(5 * time.Second)
	writeTestPNGColor(t, pngPath, color.RGBA{R: 0xee, G: 0x44, B: 0x22, A: 0xff})
	pl5, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("content-change plan: %v", err)
	}
	t.Logf("content-change plan:\n%s", pl5.RenderText(false))
	if len(pl5.Changes) != 1 || pl5.Changes[0].Action != provider.ActionUpdate {
		t.Fatalf("want an in-place update for an Image content change, got:\n%s", pl5.RenderText(false))
	}
	if err := apply.Apply(ctx, p, b, s, pl5); err != nil {
		t.Fatalf("content update apply: %v", err)
	}
	if r, _ := s.Find("roblox_asset.icon"); r.ID != oldID {
		t.Fatalf("an Image content update must keep the id: %s -> %s", oldID, r.ID)
	}
	pl6, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("post-update re-plan: %v", err)
	}
	if pl6.HasChanges() {
		t.Fatalf("expected convergence after the content update, got changes:\n%s", pl6.RenderText(false))
	}

	// Type change: Roblox can't change an asset's type, so Image -> Decal must
	// replace (new upload, new id), leaving the old asset in place.
	desired[0].Attributes["asset_type"] = cty.StringVal("Decal")
	pl7r, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("type-change plan: %v", err)
	}
	t.Logf("type-change plan:\n%s", pl7r.RenderText(false))
	if len(pl7r.Changes) != 1 || pl7r.Changes[0].Action != provider.ActionReplace || !pl7r.Changes[0].AbandonsOld {
		t.Fatalf("want an abandoning replace forced by asset_type, got:\n%s", pl7r.RenderText(false))
	}
	if err := apply.Apply(ctx, p, b, s, pl7r); err != nil {
		t.Fatalf("replace apply: %v", err)
	}
	newRes, _ := s.Find("roblox_asset.icon")
	if newRes.ID == oldID {
		t.Fatalf("replace must produce a new asset id, still %s", oldID)
	}
	t.Logf("replaced asset %s -> %s", oldID, newRes.ID)
	if _, err := p.Resources()["roblox_asset"].Read(ctx, oldID); err != nil {
		t.Fatalf("the old asset must still exist after the replace: %v", err)
	}
	pl8r, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("post-replace re-plan: %v", err)
	}
	if pl8r.HasChanges() {
		t.Fatalf("expected convergence after replace, got changes:\n%s", pl8r.RenderText(false))
	}

	// Import: adopt the old (now unmanaged) asset at a second address, with a
	// file whose content differs from what was uploaded. Import records the
	// file's hash as the baseline, so the next plan must not replace it (the
	// "adopt existing assets without re-uploading" case).
	adoptPath := filepath.Join(t.TempDir(), "adopted.png")
	writeTestPNGColor(t, adoptPath, color.RGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff})
	withAdopted := append(desired, plan.DesiredResource{Type: "roblox_asset", Name: "adopted", Attributes: provider.Attributes{
		"file":            cty.StringVal(adoptPath),
		"display_name":    cty.StringVal("Baseplate Live Test Icon (renamed)"),
		"description":     cty.StringVal("Updated by Baseplate's live asset test"),
		"asset_type":      cty.StringVal("Image"),
		"creator_user_id": cty.NumberIntVal(ownerID),
	}})
	if err := apply.Import(ctx, p, b, s, "roblox_asset.adopted", oldID, withAdopted); err != nil {
		t.Fatalf("import: %v", err)
	}
	pl7, err := plan.Build(ctx, p, s, withAdopted)
	if err != nil {
		t.Fatalf("post-import plan: %v", err)
	}
	if pl7.HasChanges() {
		t.Fatalf("a plan right after import must not replace or change anything, got:\n%s", pl7.RenderText(false))
	}

	// NOTE: destroy runs in t.Cleanup. Image assets are not archivable via Open
	// Cloud, so destroy unmanages the asset in place (drops it from state) and
	// leaves it in the account: one orphaned test image per live run, by design.
	t.Log("live asset lifecycle OK: uploaded via multipart+operation-poll, read back, converged; Image content change updated in place (same id) and converged; asset_type change replaced it with a new id and converged; import adopted without a replace; destroy unmanages (image assets aren't archivable)")
}
