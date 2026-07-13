package roblox

import (
	"context"
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

// TestLiveGamePassLifecycle runs plan→apply→converge→update→destroy for a game
// pass against the real Roblox API. Skipped unless BASEPLATE_LIVE=1.
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveGamePass -v
func TestLiveGamePassLifecycle(t *testing.T) {
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
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live.bpstate"))
	s := state.New()

	desired := func(name string, price int64) []plan.DesiredResource {
		return []plan.DesiredResource{
			{Type: "roblox_experience", Name: "main", Attributes: provider.Attributes{
				"universe_id":     cty.StringVal(universe),
				"desktop_enabled": cty.True,
			}},
			{Type: "roblox_game_pass", Name: "vip",
				DependsOn: []string{"roblox_experience.main"},
				Attributes: provider.Attributes{
					"name":        cty.StringVal(name),
					"price":       cty.NumberIntVal(price),
					"description": cty.StringVal("Baseplate live game-pass test"),
				},
				Refs: map[string]provider.Ref{"universe_id": {Address: "roblox_experience.main", Attr: "id"}},
			},
		}
	}

	// Create + converge.
	pl, err := plan.Build(ctx, p, s, desired("Baseplate Live VIP", 100))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply: %v", err)
	}
	pl2, err := plan.Build(ctx, p, s, desired("Baseplate Live VIP", 100))
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if pl2.HasChanges() {
		t.Fatalf("expected convergence, got:\n%s", pl2.RenderText(false))
	}

	// Update (reprice) + converge.
	pl3, err := plan.Build(ctx, p, s, desired("Baseplate Live VIP", 150))
	if err != nil {
		t.Fatalf("plan update: %v", err)
	}
	if !pl3.HasChanges() {
		t.Fatal("expected a price-change update")
	}
	if err := apply.Apply(ctx, p, b, s, pl3); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	pl4, err := plan.Build(ctx, p, s, desired("Baseplate Live VIP", 150))
	if err != nil {
		t.Fatalf("re-plan update: %v", err)
	}
	if pl4.HasChanges() {
		t.Fatalf("expected convergence after update, got:\n%s", pl4.RenderText(false))
	}

	// Destroy (off-sale unmanage; leaves the pass, drops from state).
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

	t.Log("live game-pass lifecycle OK: create→converge→update→converge→destroy")
}
