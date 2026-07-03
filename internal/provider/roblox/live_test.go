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

// TestLiveExperienceAndProductLifecycle runs the full plan→apply→converge→destroy
// lifecycle against the real Roblox Open Cloud API, including a cross-resource
// reference (the product's universe_id references the experience's id, resolved
// at apply time). It is SKIPPED unless BASEPLATE_LIVE=1 and credentials are set,
// so it never runs in CI or a normal `go test ./...`.
//
// Run it with:
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run Live -v
func TestLiveExperienceAndProductLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 (+ ROBLOX_API_KEY, BASEPLATE_TEST_UNIVERSE) to run the live test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	universe := os.Getenv("BASEPLATE_TEST_UNIVERSE")
	if key == "" || universe == "" {
		t.Fatal("ROBLOX_API_KEY and BASEPLATE_TEST_UNIVERSE must be set")
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live.tfstate"))
	s := state.New()

	desired := []plan.DesiredResource{
		{Type: "roblox_experience", Name: "main", Attributes: provider.Attributes{
			"universe_id":     cty.StringVal(universe),
			"desktop_enabled": cty.True,
		}},
		{Type: "roblox_developer_product", Name: "gems",
			DependsOn: []string{"roblox_experience.main"},
			Attributes: provider.Attributes{
				"name":        cty.StringVal("Baseplate Live Test Gems"),
				"price":       cty.NumberIntVal(25),
				"description": cty.StringVal("Created by Baseplate's live integration test"),
			},
			Refs: map[string]provider.Ref{"universe_id": {Address: "roblox_experience.main", Attr: "id"}},
		},
	}

	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	t.Logf("initial plan:\n%s", pl.RenderText(false))
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Convergence: a second plan against the resulting state must be a no-op.
	pl2, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if pl2.HasChanges() {
		t.Fatalf("expected convergence, got changes:\n%s", pl2.RenderText(false))
	}

	// Destroy: product first (depends on experience), then experience.
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

	t.Log("live lifecycle OK: experience adopted+configured, product created via resolved reference, converged")
}
