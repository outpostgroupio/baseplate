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

// TestLiveImportBlockExperience exercises the declarative import path end to end:
// ResolveImports rewrites the create into an import, apply adopts + reconciles,
// and a re-plan converges. Read-only remotely for the adopt (Read + local state
// write); the experience's writable flags aren't declared, so no remote change.
// SKIPPED unless BASEPLATE_LIVE=1 + ROBLOX_API_KEY + BASEPLATE_TEST_UNIVERSE.
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run Live -v
func TestLiveImportBlockExperience(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live import-block test")
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
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-import-block.bpstate"))
	s := state.New()

	desired := []plan.DesiredResource{
		{Type: "roblox_experience", Name: "main", Attributes: provider.Attributes{
			"universe_id": cty.StringVal(universe)}},
	}
	imports := []plan.ImportBlock{{Address: "roblox_experience.main", ID: universe}}

	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := plan.ResolveImports(ctx, p, s, pl, imports); err != nil {
		t.Fatalf("resolve imports: %v", err)
	}
	if !pl.HasChanges() {
		t.Fatal("expected a pending import")
	}
	t.Logf("plan:\n%s", pl.RenderText(false))
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply: %v", err)
	}

	pl2, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if err := plan.ResolveImports(ctx, p, s, pl2, imports); err != nil {
		t.Fatalf("re-resolve imports: %v", err)
	}
	if pl2.HasChanges() {
		t.Fatalf("post-import plan must converge, got:\n%s", pl2.RenderText(false))
	}

	t.Log("live import-block OK: experience adopted via import {}, plan converged")
}
