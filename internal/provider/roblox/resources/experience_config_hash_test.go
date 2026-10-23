package resources

import (
	"context"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// staleHashConfig rejects the first `stale` publishes with a hash mismatch (as
// seen live right after a PATCH) and records the hashes it was given.
type staleHashConfig struct {
	*fakeConfig
	stale  int
	hashes []string
}

func (s *staleHashConfig) Publish(ctx context.Context, u, r, h, m, st string) (int64, error) {
	s.hashes = append(s.hashes, h)
	if len(s.hashes) <= s.stale {
		return 0, &opencloud.APIError{Status: 400, Message: "The provided draft hash does not match the current draft."}
	}
	return s.fakeConfig.Publish(ctx, u, r, h, m, st)
}

func (s *staleHashConfig) GetDraft(ctx context.Context, u, r string) (*opencloud.ConfigDraft, error) {
	d, err := s.fakeConfig.GetDraft(ctx, u, r)
	if d != nil && len(s.hashes) > 0 {
		cp := *d
		cp.Hash = "FRESH"
		return &cp, err
	}
	return d, err
}

func TestConfigStaleDraftHashIsRereadAndRepublished(t *testing.T) {
	s := &staleHashConfig{fakeConfig: stageFixture(), stale: 1}
	r := &experienceConfig{cc: s, sleep: func(time.Duration) {}}
	if _, err := r.Update(context.Background(), "42", configDesired(cty.ObjectVal(map[string]cty.Value{"motd": cty.StringVal("yo")}))); err != nil {
		t.Fatal(err)
	}
	if len(s.hashes) != 2 || s.hashes[0] != "H" || s.hashes[1] != "FRESH" {
		t.Fatalf("publish hashes = %v", s.hashes)
	}
}

func TestConfigPersistentStaleHashFailsAndDiscards(t *testing.T) {
	s := &staleHashConfig{fakeConfig: stageFixture(), stale: 100}
	r := &experienceConfig{cc: s, sleep: func(time.Duration) {}}
	_, err := r.Update(context.Background(), "42", configDesired(cty.ObjectVal(map[string]cty.Value{"motd": cty.StringVal("yo")})))
	if !opencloud.IsDraftHashMismatch(err) {
		t.Fatalf("err = %v", err)
	}
	if len(s.hashes) != stageAttempts+1 || len(s.discarded) == 0 {
		t.Fatalf("hashes=%v discarded=%v", s.hashes, s.discarded)
	}
}
