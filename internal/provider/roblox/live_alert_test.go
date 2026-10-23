package roblox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// TestLiveAnalyticsAlertLifecycle creates a real analytics alert, converges,
// updates it (threshold, description, filters, disable), converges, destroys it
// and verifies it is GONE. SKIPPED unless BASEPLATE_LIVE=1 and ROBLOX_API_KEY +
// BASEPLATE_TEST_UNIVERSE are set; also SKIPPED (not failed) when the key lacks
// the universe.analytics.alert:read/write scopes or the experience isn't
// eligible for alerts (401/403 on a preflight list).
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveAnalyticsAlert -v
func TestLiveAnalyticsAlertLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live analytics alert test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	universe := os.Getenv("BASEPLATE_TEST_UNIVERSE")
	if key == "" || universe == "" {
		t.Skip("set ROBLOX_API_KEY and BASEPLATE_TEST_UNIVERSE")
	}
	ctx := context.Background()
	ac := opencloud.NewAnalyticsAlertClient(opencloud.New(key, rootBase))

	// Preflight: skip cleanly when the key can't use the alerts API at all.
	if _, err := ac.List(ctx, universe); err != nil {
		if opencloud.IsAlertsNotEligible(err) {
			t.Skipf("universe not eligible for analytics alerts: %v", err)
		}
		var ae *opencloud.APIError
		if errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
			t.Skipf("API key lacks universe.analytics.alert:read/write for universe %s (or the experience isn't alert-eligible): %v", universe, err)
		}
		t.Fatalf("preflight list: %v", err)
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-alert.bpstate"))
	s := state.New()

	// Always delete whatever we created, even if an assertion fails midway.
	t.Cleanup(func() {
		for _, r := range s.Resources {
			if r.Type != "roblox_analytics_alert" {
				continue
			}
			if i := strings.IndexByte(r.ID, '/'); i > 0 {
				if err := ac.Delete(context.Background(), universe, r.ID[i+1:]); err != nil && !opencloud.IsNotFound(err) {
					t.Errorf("cleanup: delete alert %s: %v", r.ID, err)
				}
			}
		}
	})

	name := fmt.Sprintf("Baseplate live test %d", time.Now().Unix())
	mk := func(threshold int64, description string, filterVals []string, enabled bool) []plan.DesiredResource {
		vals := make([]cty.Value, 0, len(filterVals))
		for _, v := range filterVals {
			vals = append(vals, cty.StringVal(v))
		}
		return []plan.DesiredResource{{Type: "roblox_analytics_alert", Name: "a", Attributes: provider.Attributes{
			"universe_id":             cty.StringVal(universe),
			"name":                    cty.StringVal(name),
			"description":             cty.StringVal(description),
			"metric":                  cty.StringVal("ClientCrashRate15m"),
			"severity":                cty.StringVal("SEV_2"),
			"interval":                cty.StringVal("OneMinute"),
			"consecutive_occurrences": cty.NumberIntVal(10),
			"operator":                cty.StringVal("Gt"),
			"threshold":               cty.NumberIntVal(threshold), // far above any real crash rate: never fires
			"filters":                 cty.MapVal(map[string]cty.Value{"Platform": cty.SetVal(vals)}),
			"enabled":                 cty.BoolVal(enabled),
		}}}
	}
	run := func(step string, desired []plan.DesiredResource, wantChanges bool) {
		t.Helper()
		pl, err := plan.Build(ctx, p, s, desired)
		if err != nil {
			t.Fatalf("%s: plan: %v", step, err)
		}
		if pl.HasChanges() != wantChanges {
			t.Fatalf("%s: HasChanges = %v, want %v:\n%s", step, pl.HasChanges(), wantChanges, pl.RenderText(false))
		}
		if wantChanges {
			t.Logf("%s plan:\n%s", step, pl.RenderText(false))
			if err := apply.Apply(ctx, p, b, s, pl); err != nil {
				t.Fatalf("%s: apply: %v", step, err)
			}
		}
	}

	v1 := mk(90, "Created by Baseplate's live integration test", []string{"Phone", "Tablet"}, true)
	run("create", v1, true)
	run("converge", v1, false)

	v2 := mk(95, "Updated by Baseplate's live integration test", []string{"Phone"}, false)
	run("update", v2, true)
	run("converge after update", v2, false)

	id := s.Resources[0].ID
	alertID := id[strings.IndexByte(id, '/')+1:]

	run("destroy", nil, true)
	list, err := ac.List(ctx, universe, alertID)
	if err != nil {
		t.Fatalf("verify list: %v", err)
	}
	for _, a := range list {
		if a.ID == alertID {
			t.Fatalf("alert %s still exists after destroy", alertID)
		}
	}
	t.Log("live analytics alert lifecycle OK: created, converged, updated, converged, destroyed and verified gone")
}
