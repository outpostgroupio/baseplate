package resources

import (
	"context"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

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

func TestConfigApplyWaitsForPublishedVersionToBeReadable(t *testing.T) {
	l := &laggyConfig{fakeConfig: &fakeConfig{}, stale: 2}
	var slept int
	r := &experienceConfig{cc: l, sleep: func(time.Duration) { slept++ }}
	if _, err := r.Update(context.Background(), "42", configDesired(sampleEntries)); err != nil {
		t.Fatal(err)
	}
	if l.reads != 3 || slept != 2 {
		t.Fatalf("reads=%d slept=%d, want 3 reads / 2 sleeps", l.reads, slept)
	}
}

func TestConfigGradualRolloutDoesNotWait(t *testing.T) {
	l := &laggyConfig{fakeConfig: &fakeConfig{}, stale: 100}
	r := &experienceConfig{cc: l, sleep: func(time.Duration) { t.Fatal("must not wait for a gradual rollout") }}
	d := configDesired(sampleEntries)
	d["deployment_strategy"] = cty.StringVal("GradualRollout")
	if _, err := r.Update(context.Background(), "42", d); err != nil {
		t.Fatal(err)
	}
}

func TestConfigWaitIsBounded(t *testing.T) {
	l := &laggyConfig{fakeConfig: &fakeConfig{}, stale: 1000}
	var slept int
	r := &experienceConfig{cc: l, sleep: func(time.Duration) { slept++ }}
	if _, err := r.Update(context.Background(), "42", configDesired(sampleEntries)); err != nil {
		t.Fatal(err)
	}
	if slept != 10 {
		t.Fatalf("slept=%d, want a bounded 10", slept)
	}
}
