package roblox

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// TestLiveExperienceExtras drives roblox_experience's private-server price and
// Discord social link through set → partial (sub-path) update → clear, asserting
// convergence after each apply, then restores the original values. The `public`
// toggle is exercised only if the key has legacy-universe:manage (probed with a
// no-op call to the current visibility); otherwise that part is skipped.
//
// SKIPPED unless BASEPLATE_LIVE=1 and ROBLOX_API_KEY + BASEPLATE_TEST_UNIVERSE
// are set. Needs universe:write (and legacy-universe:manage for `public`).
//
//	set -a; . ./.env; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveExperienceExtras -v
func TestLiveExperienceExtras(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live experience-extras test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	universe := os.Getenv("BASEPLATE_TEST_UNIVERSE")
	if key == "" || universe == "" {
		t.Skip("set ROBLOX_API_KEY and BASEPLATE_TEST_UNIVERSE")
	}
	ctx := context.Background()
	uc := opencloud.NewUniverseClient(opencloud.New(key, rootBase))

	orig, err := uc.Get(ctx, universe)
	if err != nil {
		t.Fatalf("read universe: %v", err)
	}
	t.Cleanup(func() {
		// Restore only the fields this test owns. A nil price / absent link in
		// orig is restored by masking the field and omitting it.
		restore := opencloud.Universe{ID: universe, PrivateServerPriceRobux: orig.PrivateServerPriceRobux}
		if l, ok := orig.SocialLinks["discordSocialLink"]; ok {
			restore.SocialLinks = map[string]opencloud.SocialLink{"discordSocialLink": l}
		}
		if _, err := uc.Update(ctx, restore, []string{"discordSocialLink", "privateServerPriceRobux"}); err != nil {
			t.Errorf("restore universe: %v", err)
		}
		if cur, err := uc.Get(ctx, universe); err == nil && cur.Visibility != orig.Visibility && orig.Visibility != "" {
			if err := uc.SetPublic(ctx, universe, orig.Visibility == "PUBLIC"); err != nil {
				t.Errorf("restore visibility to %s: %v", orig.Visibility, err)
			}
		}
	})

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-experience-extras.bpstate"))
	s := state.New()
	// tryStep plans and applies, returning an apply error to the caller; on
	// success it asserts a re-plan converges.
	tryStep := func(t *testing.T, name string, attrs provider.Attributes) error {
		t.Helper()
		attrs["universe_id"] = cty.StringVal(universe)
		desired := []plan.DesiredResource{{Type: "roblox_experience", Name: "main", Attributes: attrs}}
		pl, err := plan.Build(ctx, p, s, desired)
		if err != nil {
			t.Fatalf("%s: plan: %v", name, err)
		}
		t.Logf("%s plan:\n%s", name, pl.RenderText(false))
		if err := apply.Apply(ctx, p, b, s, pl); err != nil {
			return err
		}
		pl2, err := plan.Build(ctx, p, s, desired)
		if err != nil {
			t.Fatalf("%s: re-plan: %v", name, err)
		}
		if pl2.HasChanges() {
			t.Fatalf("%s: expected convergence, got:\n%s", name, pl2.RenderText(false))
		}
		return nil
	}
	step := func(t *testing.T, name string, attrs provider.Attributes) {
		t.Helper()
		if err := tryStep(t, name, attrs); err != nil {
			t.Fatalf("%s: apply: %v", name, err)
		}
	}

	price := int64(25)
	if orig.PrivateServerPriceRobux != nil && *orig.PrivateServerPriceRobux == price {
		price = 30
	}
	const uri = "https://discord.gg/roblox"

	// 1. Enable private servers at a price and create a Discord link.
	step(t, "set", provider.Attributes{
		"private_server_price": cty.NumberIntVal(price),
		"discord_link_uri":     cty.StringVal(uri),
		"discord_link_title":   cty.StringVal("Baseplate live test"),
	})
	got, err := uc.Get(ctx, universe)
	if err != nil {
		t.Fatal(err)
	}
	if got.PrivateServerPriceRobux == nil || *got.PrivateServerPriceRobux != price {
		t.Fatalf("price not applied: %v", got.PrivateServerPriceRobux)
	}
	if l := got.SocialLinks["discordSocialLink"]; l.URI != uri || l.Title != "Baseplate live test" {
		t.Fatalf("discord link not applied: %+v", l)
	}

	// 2. Title-only change (sub-path mask) leaves the uri alone; enabled=true
	// without a price is a no-op while private servers are already on.
	step(t, "retitle", provider.Attributes{
		"private_servers_enabled": cty.True,
		"discord_link_title":      cty.StringVal("Baseplate live test 2"),
	})
	got, err = uc.Get(ctx, universe)
	if err != nil {
		t.Fatal(err)
	}
	if l := got.SocialLinks["discordSocialLink"]; l.URI != uri || l.Title != "Baseplate live test 2" {
		t.Fatalf("sub-path title update wrong: %+v", l)
	}

	// 3. Disable private servers and remove the link.
	step(t, "clear", provider.Attributes{
		"private_servers_enabled": cty.False,
		"discord_link_uri":        cty.StringVal(""),
	})
	got, err = uc.Get(ctx, universe)
	if err != nil {
		t.Fatal(err)
	}
	if got.PrivateServerPriceRobux != nil {
		t.Fatalf("private servers still enabled: %v", *got.PrivateServerPriceRobux)
	}
	if l, ok := got.SocialLinks["discordSocialLink"]; ok {
		t.Fatalf("discord link not removed: %+v", l)
	}

	// 4. public, as a subtest so a skip here doesn't hide steps 1-3 passing.
	// The parent's t.Cleanup restores visibility whatever happens.
	t.Run("public", func(t *testing.T) {
		if orig.Visibility != "PUBLIC" && orig.Visibility != "PRIVATE" {
			t.Skipf("unexpected visibility %q", orig.Visibility)
		}
		isPublic := orig.Visibility == "PUBLIC"
		// Probe the scope with a no-op call to the current visibility
		// (deactivate on a PRIVATE universe is accepted: 200 {}).
		if err := uc.SetPublic(ctx, universe, isPublic); err != nil {
			var apiErr *opencloud.APIError
			if errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden) {
				t.Skipf("key lacks legacy-universe:manage (%v)", err)
			}
			t.Fatalf("public probe: %v", err)
		}
		// Roblox can refuse activation for a key that has the scope (403 code
		// 3, "You are not authorized to configure this universe."). That's a
		// limit of the test universe/account, not a Baseplate bug: skip.
		if err := tryStep(t, "flip-public", provider.Attributes{"public": cty.BoolVal(!isPublic)}); err != nil {
			var apiErr *opencloud.APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden && strings.Contains(err.Error(), "refused to make") {
				t.Skipf("Roblox refused the flip for this universe (%v)", err)
			}
			t.Fatalf("flip-public: apply: %v", err)
		}
		if got, err := uc.Get(ctx, universe); err != nil || (got.Visibility == "PUBLIC") == isPublic {
			t.Fatalf("visibility not flipped on Roblox: %q (%v)", got.Visibility, err)
		}
		step(t, "restore-public", provider.Attributes{"public": cty.BoolVal(isPublic)})
	})
}
