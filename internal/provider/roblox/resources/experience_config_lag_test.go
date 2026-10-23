package resources

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// Unit tests never really sleep (freshPublished/awaitVersion back off between reads).
func init() { configSleep = func(time.Duration) {} }

// laggyConfig serves the pre-publish config for the first `stale` reads after a
// publish (the read-after-publish lag observed live).
type laggyConfig struct {
	*fakeConfig
	stale    int
	oldValue opencloud.ConfigValues
	reads    int
}

func (l *laggyConfig) Publish(ctx context.Context, u, r, h, m, s string) (int64, error) {
	l.oldValue = l.fakeConfig.published
	return l.fakeConfig.Publish(ctx, u, r, h, m, s)
}

func (l *laggyConfig) GetPublished(ctx context.Context, u, r string) (opencloud.ConfigValues, error) {
	if len(l.publishes) > 0 {
		l.reads++
		if l.reads <= l.stale {
			return l.oldValue, nil
		}
	}
	return l.fakeConfig.GetPublished(ctx, u, r)
}

// sleepsAfterPublish counts sleeps that happen once something was published.
func sleepsAfterPublish(f *fakeConfig, n *int) func(time.Duration) {
	return func(time.Duration) {
		if len(f.publishes) > 0 {
			*n++
		}
	}
}

func TestConfigApplyWaitsForPublishedVersionToBeReadable(t *testing.T) {
	l := &laggyConfig{fakeConfig: &fakeConfig{}, stale: 2}
	var slept int
	r := &experienceConfig{cc: l, sleep: sleepsAfterPublish(l.fakeConfig, &slept)}
	if _, err := r.Update(context.Background(), "42", configDesired(sampleEntries)); err != nil {
		t.Fatal(err)
	}
	if l.reads != 3 || slept != 2 {
		t.Fatalf("reads=%d slept=%d, want 3 reads / 2 sleeps", l.reads, slept)
	}
}

func TestConfigGradualRolloutDoesNotWait(t *testing.T) {
	l := &laggyConfig{fakeConfig: &fakeConfig{}, stale: 100}
	var slept int
	r := &experienceConfig{cc: l, sleep: sleepsAfterPublish(l.fakeConfig, &slept)}
	d := configDesired(sampleEntries)
	d["deployment_strategy"] = cty.StringVal("GradualRollout")
	if _, err := r.Update(context.Background(), "42", d); err != nil {
		t.Fatal(err)
	}
	if slept != 0 || l.reads != 0 {
		t.Fatalf("must not wait for a gradual rollout (slept=%d reads=%d)", slept, l.reads)
	}
}

func TestConfigWaitIsBounded(t *testing.T) {
	l := &laggyConfig{fakeConfig: &fakeConfig{}, stale: 1000}
	var slept int
	r := &experienceConfig{cc: l, sleep: sleepsAfterPublish(l.fakeConfig, &slept)}
	if _, err := r.Update(context.Background(), "42", configDesired(sampleEntries)); err != nil {
		t.Fatal(err)
	}
	if slept != 10 {
		t.Fatalf("slept=%d, want a bounded 10", slept)
	}
}

// flappingConfig serves `stale` (an older version) on the first read of every
// freshPublished batch, as reads flap between versions after a publish.
type flappingConfig struct {
	*fakeConfig
	stale opencloud.ConfigValues
	reads int
}

func (f *flappingConfig) GetPublished(ctx context.Context, u, r string) (opencloud.ConfigValues, error) {
	f.reads++
	if f.reads == 1 {
		return f.stale, nil
	}
	return f.fakeConfig.GetPublished(ctx, u, r)
}

// The keys to delete come from the newest of several reads, not the first.
func TestConfigFreshReadPicksNewestVersion(t *testing.T) {
	f := &flappingConfig{
		fakeConfig: &fakeConfig{published: opencloud.ConfigValues{Version: 5, Entries: raws(map[string]string{"a": `1.0`, "b": `2.0`})}},
		stale:      opencloud.ConfigValues{Version: 4, Entries: raws(map[string]string{"a": `1.0`})},
	}
	r := &experienceConfig{cc: f, sleep: func(time.Duration) {}}
	if err := r.Delete(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	if p := f.patches[0]; len(p) != 2 {
		t.Fatalf("patch = %v, want both keys nulled", p)
	}
}

// staleBaseConfig publishes a PATCH as if it were computed from an old read:
// the first publish leaves `extra` behind, which the reconcile round removes.
type staleBaseConfig struct {
	*fakeConfig
	published int
}

func (s *staleBaseConfig) Publish(ctx context.Context, u, r, h, m, st string) (int64, error) {
	v, err := s.fakeConfig.Publish(ctx, u, r, h, m, st)
	s.published++
	if s.published == 1 {
		s.fakeConfig.published.Entries["extra"] = json.RawMessage(`true`)
	}
	return v, err
}

func TestConfigLeftoverKeysAreRemovedInAFollowUpRound(t *testing.T) {
	s := &staleBaseConfig{fakeConfig: &fakeConfig{published: opencloud.ConfigValues{Version: 1, Entries: raws(map[string]string{"a": `1.0`})}}}
	r := &experienceConfig{cc: s, sleep: func(time.Duration) {}}
	if _, err := r.Update(context.Background(), "42", configDesired(cty.ObjectVal(map[string]cty.Value{"a": cty.NumberIntVal(2)}))); err != nil {
		t.Fatal(err)
	}
	if len(s.publishes) != 2 {
		t.Fatalf("publishes = %d, want a follow-up round", len(s.publishes))
	}
	if p := s.patches[1]; len(p) != 1 || p["extra"] != nil {
		t.Fatalf("follow-up patch = %v", p)
	}
	if _, ok := s.fakeConfig.published.Entries["extra"]; ok {
		t.Fatal("leftover key must be removed")
	}
}
