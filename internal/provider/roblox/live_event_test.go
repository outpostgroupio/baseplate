package roblox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// TestLiveExperienceEventLifecycle runs create→converge→update→converge→destroy
// →verify-gone for a PRIVATE experience event scheduled weeks in the future, so
// nobody sees it. Skipped unless BASEPLATE_LIVE=1, and skipped (not failed) when
// the key lacks the universe.event:read/write scopes.
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveExperienceEvent -v
func TestLiveExperienceEventLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 (+ ROBLOX_API_KEY, BASEPLATE_TEST_UNIVERSE) to run")
	}
	key, universe := os.Getenv("ROBLOX_API_KEY"), os.Getenv("BASEPLATE_TEST_UNIVERSE")
	if key == "" || universe == "" {
		t.Fatal("ROBLOX_API_KEY and BASEPLATE_TEST_UNIVERSE must be set")
	}
	ctx := context.Background()
	ec := opencloud.NewGameEventClient(opencloud.New(key, rootBase))

	// Scope probe: reading a nonexistent event is a 404 with the read scope and a
	// 401/403 ("Scope not authorized.") without it.
	if _, err := ec.Get(ctx, "1"); err != nil && !opencloud.IsNotFound(err) {
		var apiErr *opencloud.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) {
			t.Skipf("API key lacks the universe.event:read/write scopes: %v", err)
		}
		t.Fatalf("scope probe: %v", err)
	}

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live.bpstate"))
	s := state.New()

	start := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Hour)
	desired := func(title string, startOffset time.Duration) []plan.DesiredResource {
		st := start.Add(startOffset)
		return []plan.DesiredResource{{
			Type: "roblox_experience_event", Name: "live",
			Attributes: provider.Attributes{
				"universe_id": cty.StringVal(universe),
				"title":       cty.StringVal(title),
				"subtitle":    cty.StringVal("Baseplate live test"),
				"description": cty.StringVal("Created by Baseplate's live integration test; safe to delete."),
				// A non-UTC offset proves normalisation converges against Roblox's read-back.
				"start_time": cty.StringVal(st.In(time.FixedZone("PST", -8*3600)).Format(time.RFC3339)),
				"end_time":   cty.StringVal(st.Add(2 * time.Hour).Format(time.RFC3339)),
				"visibility": cty.StringVal("private"),
				"categories": cty.ListVal([]cty.Value{cty.StringVal("activity")}),
			},
		}}
	}

	// Safety net: delete the event directly if the test fails before destroy.
	t.Cleanup(func() {
		if r, ok := s.Find("roblox_experience_event.live"); ok {
			if err := ec.Delete(ctx, r.ID); err != nil && !opencloud.IsNotFound(err) {
				t.Errorf("cleanup delete %s: %v", r.ID, err)
			}
		}
	})

	step := func(name string, d []plan.DesiredResource, wantChanges bool) {
		t.Helper()
		pl, err := plan.Build(ctx, p, s, d)
		if err != nil {
			t.Fatalf("%s plan: %v", name, err)
		}
		if pl.HasChanges() != wantChanges {
			t.Fatalf("%s: HasChanges=%v, want %v:\n%s", name, pl.HasChanges(), wantChanges, pl.RenderText(false))
		}
		if wantChanges {
			if err := apply.Apply(ctx, p, b, s, pl); err != nil {
				t.Fatalf("%s apply: %v", name, err)
			}
		}
	}

	step("create", desired("Baseplate Live Event", 0), true)
	step("converge", desired("Baseplate Live Event", 0), false)
	step("update", desired("Baseplate Live Event v2", time.Hour), true)
	step("converge after update", desired("Baseplate Live Event v2", time.Hour), false)

	r, _ := s.Find("roblox_experience_event.live")
	eventID := r.ID
	step("destroy", nil, true)
	if _, err := ec.Get(ctx, eventID); !opencloud.IsNotFound(err) {
		t.Fatalf("event %s should be gone after destroy, Get err = %v", eventID, err)
	}
	t.Log("live experience-event lifecycle OK: create→converge→update→converge→destroy→gone")
}
