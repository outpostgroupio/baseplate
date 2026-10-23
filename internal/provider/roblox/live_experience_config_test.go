package roblox

import (
	"context"
	"encoding/json"
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

// liveCfgRule is a conditional rule (players in the US) used to prove that
// Baseplate preserves conditional rules and branches it doesn't manage.
const liveCfgRule = `{"rules":{"bpc1":{"tokens":[{"operand":{"attributeReference":"Country"}},{"operand":{"literalValue":{"stringValue":"US"}}},{"operator":"OPERATOR_EQ"}]}},"rulesOrder":["bpc1"]}`

// TestLiveExperienceConfigLifecycle drives roblox_experience_config against the
// real Experience Config API:
//
//  1. records the repository's published config (restored in t.Cleanup),
//  2. seeds it with an undeclared key, a key with a conditional branch, and a
//     conditional rule,
//  3. checks create refuses to silently delete the undeclared key,
//  4. adopts it with an import block + apply (removing the undeclared key while
//     preserving the rule and the kept key's branch), and converges,
//  5. updates (change/add/remove keys, nested objects), and converges,
//  6. destroys — which must leave the live config untouched (unmanage only).
//
// SKIPPED unless BASEPLATE_LIVE=1 and ROBLOX_API_KEY + BASEPLATE_TEST_UNIVERSE are
// set (the key needs universe:read + universe:write). Publishes are Immediate.
//
//	set -a; . ./.env.local; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveExperienceConfig -v
func TestLiveExperienceConfigLifecycle(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 to run the live experience config test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	universe := os.Getenv("BASEPLATE_TEST_UNIVERSE")
	if key == "" || universe == "" {
		t.Skip("set ROBLOX_API_KEY and BASEPLATE_TEST_UNIVERSE")
	}
	const repo = opencloud.DefaultConfigRepository
	ctx := context.Background()
	cc := opencloud.NewExperienceConfigClient(opencloud.New(key, rootBase))

	if d, err := cc.GetDraft(ctx, universe, repo); err != nil {
		t.Fatalf("get draft: %v", err)
	} else if d != nil {
		t.Fatalf("repository has a pending draft (keys %v); publish or discard it before running this test", d.Entries)
	}
	orig, err := cc.GetPublished(ctx, universe, repo)
	if err != nil {
		t.Fatalf("record original config: %v", err)
	}
	t.Logf("original config: version %d, %d entries, rules=%s", orig.Version, len(orig.Entries), orig.ConditionalRules)
	publishRaw := func(what string, entries map[string]json.RawMessage, rules json.RawMessage) {
		t.Helper()
		h, err := cc.OverwriteDraft(ctx, universe, repo, opencloud.ConfigOverwrite{Entries: entries, ConditionalRules: rules})
		if err != nil {
			t.Fatalf("%s: overwrite: %v", what, err)
		}
		v, err := cc.Publish(ctx, universe, repo, h, "Baseplate live test: "+what, "Immediate")
		if err != nil {
			if opencloud.IsEmptyDraft(err) {
				_ = cc.DiscardDraft(ctx, universe, repo, h)
				return
			}
			_ = cc.DiscardDraft(ctx, universe, repo, h)
			t.Fatalf("%s: publish: %v", what, err)
		}
		// Reads can briefly lag a publish; wait until this version is served.
		for i := 0; i < 10; i++ {
			if got, err := cc.GetPublished(ctx, universe, repo); err == nil && got.Version >= v {
				return
			}
			time.Sleep(time.Second)
		}
	}
	t.Cleanup(func() {
		publishRaw("restore original", orig.Entries, orig.ConditionalRules)
		t.Log("restored the original config")
	})

	// Seed: an undeclared key, a key with a conditional branch, and the rule.
	publishRaw("seed", map[string]json.RawMessage{
		"bp_stale":                     json.RawMessage(`1`),
		"bp_motd":                      json.RawMessage(`"seed"`),
		"bp_motd.RBX.conditional.bpc1": json.RawMessage(`"seed-us"`),
	}, json.RawMessage(liveCfgRule))

	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "live-config.bpstate"))
	s := state.New()
	addr := "roblox_experience_config.flags"
	mk := func(entries map[string]cty.Value) []plan.DesiredResource {
		return []plan.DesiredResource{{Type: "roblox_experience_config", Name: "flags", Attributes: provider.Attributes{
			"universe_id":         cty.StringVal(universe),
			"entries":             cty.ObjectVal(entries),
			"deployment_strategy": cty.StringVal("Immediate"),
		}}}
	}
	v1 := mk(map[string]cty.Value{
		"bp_motd":   cty.StringVal("hello"),
		"bp_on":     cty.True,
		"bp_health": cty.NumberIntVal(100),
		"bp_rate":   cty.MustParseNumberVal("0.15"),
		"bp_shop": cty.ObjectVal(map[string]cty.Value{
			"discount": cty.MustParseNumberVal("0.2"),
			"featured": cty.TupleVal([]cty.Value{cty.StringVal("sword"), cty.NumberIntVal(3)}),
		}),
	})

	// 3. A plain create must refuse: bp_stale is live but undeclared.
	pl, err := plan.Build(ctx, p, s, v1)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := apply.Apply(ctx, p, b, s, pl); err == nil || !strings.Contains(err.Error(), "bp_stale") {
		t.Fatalf("create over undeclared keys must refuse, got %v", err)
	}
	s = state.New()

	// 4. Adopt via an import block; the one-pass apply reconciles to v1.
	imports := []plan.ImportBlock{{Address: addr, ID: universe}}
	pl, err = plan.Build(ctx, p, s, v1)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := plan.ResolveImports(ctx, p, s, pl, imports); err != nil {
		t.Fatalf("resolve imports: %v", err)
	}
	t.Logf("import plan:\n%s", pl.RenderText(false))
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply import+reconcile: %v", err)
	}
	got, err := cc.GetPublished(ctx, universe, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Entries["bp_stale"]; ok {
		t.Fatal("undeclared key bp_stale should have been removed")
	}
	if string(got.Entries["bp_motd.RBX.conditional.bpc1"]) != `"seed-us"` {
		t.Fatalf("conditional branch of kept key not preserved; entries=%v", rawKeys(got.Entries))
	}
	if !strings.Contains(string(got.ConditionalRules), "bpc1") {
		t.Fatalf("conditional rule not preserved: %s", got.ConditionalRules)
	}
	assertConverged(t, ctx, p, s, v1, "after import")

	// 5. Update: change a value, a nested object, drop a key, add a key.
	v2 := mk(map[string]cty.Value{
		"bp_motd":   cty.StringVal("updated"),
		"bp_health": cty.NumberIntVal(250),
		"bp_shop":   cty.ObjectVal(map[string]cty.Value{"discount": cty.NumberIntVal(0), "featured": cty.EmptyTupleVal}),
		"bp_new":    cty.StringVal("added"),
	})
	pl, err = plan.Build(ctx, p, s, v2)
	if err != nil {
		t.Fatalf("plan update: %v", err)
	}
	if !pl.HasChanges() {
		t.Fatal("changing entries must plan an update")
	}
	t.Logf("update plan:\n%s", pl.RenderText(false))
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	assertConverged(t, ctx, p, s, v2, "after update")
	got, err = cc.GetPublished(ctx, universe, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Entries["bp_on"]; ok {
		t.Fatal("bp_on was removed from config and should be gone")
	}
	if string(got.Entries["bp_motd.RBX.conditional.bpc1"]) != `"seed-us"` {
		t.Fatal("conditional branch lost on update")
	}

	// 6. Destroy only unmanages: the published config must be unchanged.
	before := got.Version
	pl, err = plan.Build(ctx, p, s, nil)
	if err != nil {
		t.Fatalf("destroy plan: %v", err)
	}
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, ok := s.Find(addr); ok {
		t.Fatal("destroy must drop the resource from state")
	}
	after, err := cc.GetPublished(ctx, universe, repo)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before || string(after.Entries["bp_new"]) != `"added"` {
		t.Fatalf("destroy must not touch the live config (version %d -> %d)", before, after.Version)
	}
	t.Log("live experience config lifecycle OK: refused unsafe create, imported (removed undeclared, kept rule+branch), converged, updated, converged, destroy left config live")
}

func assertConverged(t *testing.T, ctx context.Context, p provider.Provider, s *state.State, desired []plan.DesiredResource, when string) {
	t.Helper()
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatalf("re-plan %s: %v", when, err)
	}
	if pl.HasChanges() {
		for _, c := range pl.Changes {
			for _, d := range c.AttrDiffs {
				t.Logf("diff %s: %#v => %#v", d.Name, d.Old, d.New)
			}
		}
		t.Fatalf("expected convergence %s, got:\n%s", when, pl.RenderText(false))
	}
}

func rawKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
