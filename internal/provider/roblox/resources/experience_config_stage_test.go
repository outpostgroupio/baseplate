package resources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// corruptingConfig reproduces the rare live failure: a PATCH that sent no rules
// produces a draft that also tombstones the conditional rules and branches. The
// first `bad` staged drafts are corrupted.
type corruptingConfig struct {
	*fakeConfig
	bad int
}

func (c *corruptingConfig) GetDraft(ctx context.Context, u, r string) (*opencloud.ConfigDraft, error) {
	d, err := c.fakeConfig.GetDraft(ctx, u, r)
	if d == nil || len(c.patches) == 0 || len(c.patches) > c.bad {
		return d, err
	}
	bad := &opencloud.ConfigDraft{Hash: d.Hash, Entries: map[string]json.RawMessage{}, ConditionalRules: json.RawMessage(`{"rules":{"c1":{"tokens":[]}},"rulesOrder":[]}`)}
	for k, v := range d.Entries {
		bad.Entries[k] = v
	}
	bad.Entries["motd.RBX.conditional.c1"] = json.RawMessage(`null`)
	return bad, nil
}

func stageFixture() *fakeConfig {
	return &fakeConfig{published: opencloud.ConfigValues{
		Version:          4,
		ConditionalRules: json.RawMessage(`{"rules":{"c1":{"tokens":[{"operator":"OPERATOR_EQ","operand":null}]}},"rulesOrder":["c1"]}`),
		Entries:          raws(map[string]string{"motd": `"hi"`, "motd.RBX.conditional.c1": `"hola"`, "old": `1.0`}),
	}}
}

func TestConfigCorruptedDraftIsDiscardedAndRestaged(t *testing.T) {
	c := &corruptingConfig{fakeConfig: stageFixture(), bad: 1}
	var slept int
	r := &experienceConfig{cc: c, sleep: func(time.Duration) { slept++ }}
	if _, err := r.Update(context.Background(), "42", configDesired(cty.ObjectVal(map[string]cty.Value{"motd": cty.StringVal("yo")}))); err != nil {
		t.Fatal(err)
	}
	if len(c.patches) != 2 || len(c.discarded) != 1 || len(c.publishes) != 1 || slept == 0 {
		t.Fatalf("patches=%d discarded=%d publishes=%d slept=%d", len(c.patches), len(c.discarded), len(c.publishes), slept)
	}
}

func TestConfigPersistentlyCorruptedDraftIsNeverPublished(t *testing.T) {
	c := &corruptingConfig{fakeConfig: stageFixture(), bad: 100}
	r := &experienceConfig{cc: c, sleep: func(time.Duration) {}}
	_, err := r.Update(context.Background(), "42", configDesired(cty.ObjectVal(map[string]cty.Value{"motd": cty.StringVal("yo")})))
	if err == nil || !strings.Contains(err.Error(), "motd.RBX.conditional.c1") || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("err = %v", err)
	}
	if len(c.publishes) != 0 || len(c.discarded) != stageAttempts {
		t.Fatalf("publishes=%d discarded=%d", len(c.publishes), len(c.discarded))
	}
}

func TestUnintendedChanges(t *testing.T) {
	pub := stageFixture().published
	changes := map[string]json.RawMessage{"motd": json.RawMessage(`"x"`), "old": nil}
	ok := []*opencloud.ConfigDraft{
		{Entries: raws(map[string]string{"motd": `"x"`, "old": `null`})},
		{Entries: raws(map[string]string{"old": `null`, "old.RBX.conditional.c1": `null`})}, // our delete takes its branches
		{Entries: raws(map[string]string{"motd": `"x"`}), ConditionalRules: pub.ConditionalRules},
		{Entries: raws(map[string]string{"motd": `"x"`}), ConditionalRules: json.RawMessage(`{"rulesOrder":["c1"],"rules":{"c1":{"tokens":[{"operand":null,"operator":"OPERATOR_EQ"}]}}}`)},
	}
	for i, d := range ok {
		if p := unintendedChanges(d, changes, pub); p != "" {
			t.Errorf("ok[%d]: unexpected problem %q", i, p)
		}
	}
	bad := map[string]*opencloud.ConfigDraft{
		"deletes kept branch": {Entries: raws(map[string]string{"motd.RBX.conditional.c1": `null`})},
		"deletes other key":   {Entries: raws(map[string]string{"other": `null`})},
		"changes rules":       {Entries: raws(map[string]string{}), ConditionalRules: json.RawMessage(`{"rules":{},"rulesOrder":[]}`)},
		"unreadable":          nil,
	}
	for name, d := range bad {
		if p := unintendedChanges(d, changes, pub); p == "" {
			t.Errorf("%s: expected a problem", name)
		}
	}
}

// droppingConfig publishes a clean draft but the result loses the rules and
// branches (the post-publish safety net must report it loudly).
type droppingConfig struct{ *fakeConfig }

func (d *droppingConfig) Publish(ctx context.Context, u, r, h, m, s string) (int64, error) {
	v, err := d.fakeConfig.Publish(ctx, u, r, h, m, s)
	d.published.ConditionalRules = nil
	for k := range d.published.Entries {
		if isBranchKey(k) {
			delete(d.published.Entries, k)
		}
	}
	return v, err
}

func TestConfigPostPublishLossIsReported(t *testing.T) {
	d := &droppingConfig{fakeConfig: stageFixture()}
	r := &experienceConfig{cc: d, sleep: func(time.Duration) {}}
	_, err := r.Update(context.Background(), "42", configDesired(cty.ObjectVal(map[string]cty.Value{"motd": cty.StringVal("yo")})))
	if err == nil || !strings.Contains(err.Error(), "motd.RBX.conditional.c1") || !strings.Contains(err.Error(), "conditional rules") || !strings.Contains(err.Error(), "restore version 4") {
		t.Fatalf("err = %v", err)
	}
}

func TestLostPreservedIgnoresBranchesOfDeletedKeys(t *testing.T) {
	before := opencloud.ConfigValues{Entries: raws(map[string]string{"a": `1`, "a.RBX.conditional.c1": `2`})}
	after := opencloud.ConfigValues{Entries: raws(map[string]string{})}
	if got := lostPreserved(before, after, map[string]json.RawMessage{"a": nil}); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := lostPreserved(before, after, map[string]json.RawMessage{"a": json.RawMessage(`3`)}); got != "a.RBX.conditional.c1" {
		t.Fatalf("got %q", got)
	}
}
