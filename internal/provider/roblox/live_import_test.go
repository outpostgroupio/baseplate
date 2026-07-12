package roblox

import (
	"context"
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

// TestLiveImportExperience imports a real universe into state (read-only) and
// asserts a subsequent plan converges. SKIPPED unless BASEPLATE_LIVE=1 and
// ROBLOX_API_KEY + BASEPLATE_TEST_UNIVERSE are set. Read-only remotely: no
// mutation, no residue.
//
// Run it with:
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run Live -v
func TestLiveImportExperience(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live import test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	universe := os.Getenv("BASEPLATE_TEST_UNIVERSE")
	if key == "" || universe == "" {
		t.Skip("set ROBLOX_API_KEY and BASEPLATE_TEST_UNIVERSE")
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-import.bpstate"))
	s := state.New()

	// Declare only universe_id — experience.Read echoes it, so the diff compares
	// exactly that (the bool flags are undeclared, hence not compared).
	desired := []plan.DesiredResource{
		{Type: "roblox_experience", Name: "main", Attributes: provider.Attributes{
			"universe_id": cty.StringVal(universe)}},
	}

	if err := apply.Import(ctx, p, b, s, "roblox_experience.main", universe, desired); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got, ok := s.Find("roblox_experience.main"); !ok || got.ID != universe {
		t.Fatalf("import did not record the experience: %+v", got)
	}

	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if pl.HasChanges() {
		t.Fatalf("plan after import must converge, got:\n%s", pl.RenderText(false))
	}

	t.Log("live import OK: experience adopted into state, plan converged")
}
