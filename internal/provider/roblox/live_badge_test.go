package roblox

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// TestLiveBadgeLifecycle runs plan→apply→converge→update→icon-change→destroy for a
// badge against the real Roblox API. Skipped unless BASEPLATE_LIVE=1.
//
//	set -a; . ./.env; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveBadge -v
func TestLiveBadgeLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 (+ ROBLOX_API_KEY, BASEPLATE_TEST_UNIVERSE) to run")
	}
	key, universe := os.Getenv("ROBLOX_API_KEY"), os.Getenv("BASEPLATE_TEST_UNIVERSE")
	if key == "" || universe == "" {
		t.Fatal("ROBLOX_API_KEY and BASEPLATE_TEST_UNIVERSE must be set")
	}
	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	iconA := filepath.Join(dir, "a.png")
	iconB := filepath.Join(dir, "b.png")
	writePNG(t, iconA, color.RGBA{0x33, 0x88, 0xff, 0xff})
	writePNG(t, iconB, color.RGBA{0xff, 0x33, 0x33, 0xff})

	b := backend.NewLocal(filepath.Join(dir, "live.bpstate"))
	s := state.New()

	desired := func(name string, enabled bool, icon string) []plan.DesiredResource {
		return []plan.DesiredResource{
			{Type: "roblox_experience", Name: "main", Attributes: provider.Attributes{
				"universe_id":     cty.StringVal(universe),
				"desktop_enabled": cty.True,
			}},
			{Type: "roblox_badge", Name: "welcome",
				DependsOn: []string{"roblox_experience.main"},
				Attributes: provider.Attributes{
					"name":        cty.StringVal(name),
					"description": cty.StringVal("Baseplate live badge test"),
					"enabled":     cty.BoolVal(enabled),
					"icon_file":   cty.StringVal(icon),
				},
				Refs: map[string]provider.Ref{"universe_id": {Address: "roblox_experience.main", Attr: "id"}},
			},
		}
	}

	mustConverge := func(label string, d []plan.DesiredResource) {
		pl, err := plan.Build(ctx, p, s, d)
		if err != nil {
			t.Fatalf("%s plan: %v", label, err)
		}
		if err := apply.Apply(ctx, p, b, s, pl); err != nil {
			t.Fatalf("%s apply: %v", label, err)
		}
		pl2, err := plan.Build(ctx, p, s, d)
		if err != nil {
			t.Fatalf("%s re-plan: %v", label, err)
		}
		if pl2.HasChanges() {
			t.Fatalf("%s: expected convergence, got:\n%s", label, pl2.RenderText(false))
		}
	}

	// Create + converge.
	mustConverge("create", desired("Baseplate Welcome", true, iconA))
	// Update name + disable, converge.
	mustConverge("update", desired("Baseplate Welcome v2", false, iconA))
	// Change icon (content_hash trigger), converge.
	mustConverge("icon", desired("Baseplate Welcome v2", false, iconB))

	// Destroy (disable + unmanage; leaves the badge, drops from state).
	t.Cleanup(func() {
		dpl, err := plan.Build(ctx, p, s, nil)
		if err != nil {
			t.Errorf("destroy plan: %v", err)
			return
		}
		if err := apply.Apply(ctx, p, b, s, dpl); err != nil {
			t.Errorf("destroy: %v", err)
		}
	})

	t.Log("live badge lifecycle OK: create→converge→update→converge→icon→converge→destroy")
}

func writePNG(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
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
