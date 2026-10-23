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
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// TestLivePlaceConfigAndDeviceToggles flips the experience's console/VR toggles
// and the test place's name/description/server size, asserts a re-plan
// converges, then restores the original values. SKIPPED unless BASEPLATE_LIVE=1
// and ROBLOX_API_KEY + BASEPLATE_TEST_UNIVERSE + BASEPLATE_TEST_PLACE are set.
// The key needs universe:write plus universe.place:read/write.
//
// Run it with:
//
//	set -a; . ./.env; set +a
//	go test ./internal/provider/roblox/ -run LivePlaceConfig -v
func TestLivePlaceConfigAndDeviceToggles(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live place-config test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	universe := os.Getenv("BASEPLATE_TEST_UNIVERSE")
	placeID := os.Getenv("BASEPLATE_TEST_PLACE")
	if key == "" || universe == "" || placeID == "" {
		t.Skip("set ROBLOX_API_KEY, BASEPLATE_TEST_UNIVERSE, and BASEPLATE_TEST_PLACE")
	}
	ctx := context.Background()
	root := opencloud.New(key, rootBase)
	uc := opencloud.NewUniverseClient(root)
	pc := opencloud.NewPlaceConfigClient(root)

	origU, err := uc.Get(ctx, universe)
	if err != nil {
		t.Fatalf("read universe: %v", err)
	}
	origP, err := pc.Get(ctx, universe, placeID)
	if err != nil {
		t.Fatalf("read place: %v", err)
	}
	t.Cleanup(func() {
		if origU.ConsoleEnabled != nil && origU.VREnabled != nil {
			if _, err := uc.Update(ctx, opencloud.Universe{ID: universe, ConsoleEnabled: origU.ConsoleEnabled, VREnabled: origU.VREnabled},
				[]string{"consoleEnabled", "vrEnabled"}); err != nil {
				t.Errorf("restore universe: %v", err)
			}
		}
		if _, err := pc.Update(ctx, universe, placeID, origP, []string{"description", "displayName", "serverSize"}); err != nil {
			t.Errorf("restore place: %v", err)
		}
	})

	flip := func(b *bool) bool { return b == nil || !*b }
	size := int64(12)
	if origP.ServerSize != nil && *origP.ServerSize == size {
		size = 13
	}
	desired := []plan.DesiredResource{
		{Type: "roblox_experience", Name: "main", Attributes: provider.Attributes{
			"universe_id":     cty.StringVal(universe),
			"console_enabled": cty.BoolVal(flip(origU.ConsoleEnabled)),
			"vr_enabled":      cty.BoolVal(flip(origU.VREnabled)),
		}},
		{Type: "roblox_place_config", Name: "root", Attributes: provider.Attributes{
			"universe_id":  cty.StringVal(universe),
			"place_id":     cty.StringVal(placeID),
			"display_name": cty.StringVal("Baseplate Live Test"),
			"description":  cty.StringVal("Set by Baseplate's live place-config test"),
			"server_size":  cty.NumberIntVal(size),
		}},
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-place-config.bpstate"))
	s := state.New()
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	t.Logf("initial plan:\n%s", pl.RenderText(false))
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply: %v", err)
	}

	pl2, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if pl2.HasChanges() {
		t.Fatalf("expected convergence, got:\n%s", pl2.RenderText(false))
	}

	gotP, err := pc.Get(ctx, universe, placeID)
	if err != nil {
		t.Fatal(err)
	}
	if gotP.ServerSize == nil || *gotP.ServerSize != size || gotP.DisplayName == nil || *gotP.DisplayName != "Baseplate Live Test" {
		t.Fatalf("place not updated on Roblox: %+v", gotP)
	}
}
