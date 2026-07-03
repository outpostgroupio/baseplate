package roblox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/engine/apply"
	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/state"
	"github.com/outpostgroup/baseplate/internal/state/backend"
)

// writeMinimalPlace writes a minimal well-formed .rbxlx whose content varies by
// marker (embedded as an XML comment), so the content hash changes between writes
// while the file stays valid XML.
func writeMinimalPlace(t *testing.T, path, marker string) {
	t.Helper()
	body := fmt.Sprintf(`<roblox version="4"><!--%s--></roblox>`, marker)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLivePlaceLifecycle publishes a place file to a real place, converges,
// re-publishes on a content change, and destroys (no-op). SKIPPED unless
// BASEPLATE_LIVE=1 and ROBLOX_API_KEY + BASEPLATE_TEST_UNIVERSE + BASEPLATE_TEST_PLACE
// are set.
//
// Run it with:
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run Live -v
func TestLivePlaceLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live place test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	universe := os.Getenv("BASEPLATE_TEST_UNIVERSE")
	placeID := os.Getenv("BASEPLATE_TEST_PLACE")
	if key == "" || universe == "" || placeID == "" {
		t.Skip("set ROBLOX_API_KEY, BASEPLATE_TEST_UNIVERSE, and BASEPLATE_TEST_PLACE")
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-place.tfstate"))
	s := state.New()

	fp := filepath.Join(t.TempDir(), "game.rbxlx")
	writeMinimalPlace(t, fp, "A")

	desired := []plan.DesiredResource{
		{Type: "roblox_place", Name: "root", Attributes: provider.Attributes{
			"universe_id": cty.StringVal(universe),
			"place_id":    cty.StringVal(placeID),
			"file":        cty.StringVal(fp),
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

	// Converge: same file → no changes.
	pl2, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if pl2.HasChanges() {
		t.Fatalf("expected convergence, got changes:\n%s", pl2.RenderText(false))
	}

	// Change the file → a re-publish must be planned and applied.
	writeMinimalPlace(t, fp, "B")
	pl3, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan after edit: %v", err)
	}
	if !pl3.HasChanges() {
		t.Fatal("editing the place file must plan an update")
	}
	if err := apply.Apply(ctx, p, b, s, pl3); err != nil {
		t.Fatalf("apply re-publish: %v", err)
	}

	// Converge again after the re-publish.
	pl4, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan after re-publish: %v", err)
	}
	if pl4.HasChanges() {
		t.Fatalf("expected convergence after re-publish, got:\n%s", pl4.RenderText(false))
	}

	// Destroy is a no-op (a place can't be unpublished) — nothing to clean up.
	dpl, err := plan.Build(ctx, p, s, nil)
	if err != nil {
		t.Fatalf("destroy plan: %v", err)
	}
	if err := apply.Apply(ctx, p, b, s, dpl); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	t.Log("live place lifecycle OK: published, converged, re-published on edit, converged, destroy no-op")
}
