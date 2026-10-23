package roblox

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// writeColorPNG encodes a small opaque 64x64 PNG of one colour to path, so each
// upload in a run is a distinct image.
func writeColorPNG(t *testing.T, path string, c color.RGBA) {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLiveForEachAssets exercises for_each end to end against real Roblox: a
// fileset-driven roblox_asset with two image instances is uploaded and must
// converge; then one of the uploaded ids is adopted into a fresh state through
// an import block with for_each, with no Create, and must converge too. Uploads
// count against Roblox limits, so it stays at two Image assets (never Audio).
// SKIPPED unless BASEPLATE_LIVE=1 and ROBLOX_API_KEY are set.
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveForEach -v
func TestLiveForEachAssets(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 (+ ROBLOX_API_KEY) to run the live for_each test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	if key == "" {
		t.Fatal("ROBLOX_API_KEY must be set")
	}
	owner := os.Getenv("BASEPLATE_TEST_USER")
	if owner == "" {
		owner = "5822334358"
	}
	if _, err := strconv.ParseInt(owner, 10, 64); err != nil {
		t.Fatalf("BASEPLATE_TEST_USER %q: %v", owner, err)
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	writeColorPNG(t, filepath.Join(dir, "assets", "ui", "blue.png"), color.RGBA{R: 0x33, G: 0x88, B: 0xff, A: 0xff})
	writeColorPNG(t, filepath.Join(dir, "assets", "ui", "green.png"), color.RGBA{R: 0x22, G: 0xcc, B: 0x55, A: 0xff})

	cfgPath := filepath.Join(dir, "live.bp.hcl")
	writeCfg := func(src string) {
		t.Helper()
		if err := os.WriteFile(cfgPath, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	load := func() ([]plan.DesiredResource, []plan.ImportBlock) {
		t.Helper()
		_, desired, imports, err := config.Load(cfgPath, p)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return desired, imports
	}
	planFor := func(s *state.State) *plan.Plan {
		t.Helper()
		desired, imports := load()
		pl, err := plan.Build(ctx, p, s, desired)
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if err := plan.ResolveImports(ctx, p, s, pl, imports); err != nil {
			t.Fatalf("resolve imports: %v", err)
		}
		return pl
	}
	resource := fmt.Sprintf(`
resource "roblox_asset" "icons" {
  for_each = local.files

  file            = "${path.config}/assets/${each.key}"
  display_name    = "Baseplate Live ForEach ${trimsuffix(basename(each.key), ".png")}"
  description     = "Created by Baseplate's live for_each test"
  asset_type      = "Image"
  creator_user_id = %s
}
`, owner)

	// Phase 1: upload every file found by fileset, then converge.
	writeCfg(`
locals {
  files = fileset("${path.config}/assets", "**/*.png")
}
` + resource)
	s := state.New()
	b := backend.NewLocal(filepath.Join(dir, "live-foreach.bpstate"))
	pl := planFor(s)
	if sum := pl.Summary(); sum.Add != 2 {
		t.Fatalf("expected 2 uploads, got %+v:\n%s", sum, pl.RenderText(false))
	}
	t.Logf("initial plan:\n%s", pl.RenderText(false))
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if pl2 := planFor(s); pl2.HasChanges() {
		t.Fatalf("expected convergence, got:\n%s", pl2.RenderText(false))
	}
	blue, ok := s.Find(`roblox_asset.icons["ui/blue.png"]`)
	if !ok || blue.Key != "ui/blue.png" || blue.ID == "" {
		t.Fatalf("instance ui/blue.png missing from state: %+v", s.Resources)
	}

	// Phase 2: adopt the blue asset by id into a fresh state via an import block
	// with for_each. No upload may happen, and the result must converge.
	writeCfg(fmt.Sprintf(`
locals {
  files    = toset(["ui/blue.png"])
  existing = { "ui/blue.png" = %q }
}

import {
  for_each = local.existing
  to       = roblox_asset.icons[each.key]
  id       = each.value
}
`, blue.ID) + resource)
	s2 := state.New()
	b2 := backend.NewLocal(filepath.Join(dir, "live-foreach-import.bpstate"))
	ipl := planFor(s2)
	if sum := ipl.Summary(); sum.Import != 1 || sum.Add != 0 {
		t.Fatalf("expected 1 import and no uploads, got %+v:\n%s", sum, ipl.RenderText(false))
	}
	t.Logf("import plan:\n%s", ipl.RenderText(false))
	if err := apply.Apply(ctx, p, b2, s2, ipl); err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if r, ok := s2.Find(`roblox_asset.icons["ui/blue.png"]`); !ok || r.ID != blue.ID {
		t.Fatalf("import did not adopt id %s: %+v", blue.ID, s2.Resources)
	}
	if ipl2 := planFor(s2); ipl2.HasChanges() {
		t.Fatalf("post-import plan must converge, got:\n%s", ipl2.RenderText(false))
	}

	// Image assets aren't archivable, so there's nothing to clean up remotely:
	// destroy would only unmanage them. Two orphaned test images per run, by design.
	t.Log("live for_each OK: 2 images uploaded via fileset+for_each, converged; 1 adopted by id via import for_each, converged")
}
