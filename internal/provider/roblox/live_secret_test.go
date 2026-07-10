package roblox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/engine/apply"
	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroup/baseplate/internal/state"
	"github.com/outpostgroup/baseplate/internal/state/backend"
)

// TestLiveSecretLifecycle creates a real secret, converges, rotates the value,
// changes the domain, converges, then destroys and verifies the secret is GONE.
// SKIPPED unless BASEPLATE_LIVE=1 and ROBLOX_API_KEY + BASEPLATE_TEST_UNIVERSE are
// set (the API key must carry universe.secret:read and universe.secret:write).
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveSecret -v
func TestLiveSecretLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live secret test")
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
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-secret.tfstate"))
	s := state.New()

	name := fmt.Sprintf("bp_test_%d", time.Now().Unix())
	mk := func(value, domain string) []plan.DesiredResource {
		return []plan.DesiredResource{{Type: "roblox_universe_secret", Name: "s", Attributes: provider.Attributes{
			"universe_id": cty.StringVal(universe),
			"name":        cty.StringVal(name),
			"value":       cty.StringVal(value),
			"domain":      cty.StringVal(domain),
		}}}
	}

	// Create.
	desired := mk("first-value", "*")
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply create: %v", err)
	}

	// Converge (same config -> no changes).
	pl2, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if pl2.HasChanges() {
		t.Fatalf("expected convergence, got:\n%s", pl2.RenderText(false))
	}

	// Rotate value + change domain -> must plan and apply an update.
	desired2 := mk("second-value", "discord.com")
	pl3, err := plan.Build(ctx, p, s, desired2)
	if err != nil {
		t.Fatalf("re-plan after edit: %v", err)
	}
	if !pl3.HasChanges() {
		t.Fatal("rotating value + domain must plan an update")
	}
	if err := apply.Apply(ctx, p, b, s, pl3); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	pl4, err := plan.Build(ctx, p, s, desired2)
	if err != nil {
		t.Fatalf("re-plan after update: %v", err)
	}
	if pl4.HasChanges() {
		t.Fatalf("expected convergence after update, got:\n%s", pl4.RenderText(false))
	}

	// Destroy -> the secret must be really gone (verify via a direct List).
	dpl, err := plan.Build(ctx, p, s, nil)
	if err != nil {
		t.Fatalf("destroy plan: %v", err)
	}
	if err := apply.Apply(ctx, p, b, s, dpl); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	sc := opencloud.NewSecretClient(opencloud.New(key, "https://apis.roblox.com"))
	list, err := sc.List(ctx, universe)
	if err != nil {
		t.Fatalf("verify list: %v", err)
	}
	for _, sec := range list {
		if sec.Name == name {
			t.Fatalf("secret %q still exists after destroy — hard delete did not take", name)
		}
	}
	t.Log("live secret lifecycle OK: created, converged, rotated+redomained, converged, destroyed and verified gone")
}
