package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
)

// warnProvider is the mock provider whose mock_thing reports a provider
// warning on every Read and Create (like roblox_asset's failed Decal lookup).
type warnProvider struct{ m *mock.Provider }

func (p warnProvider) Resources() map[string]provider.Resource {
	out := map[string]provider.Resource{}
	for k, v := range p.m.Resources() {
		out[k] = v
	}
	out["mock_thing"] = warnRes{out["mock_thing"]}
	return out
}

type warnRes struct{ provider.Resource }

func (r warnRes) Read(ctx context.Context, id string) (provider.Attributes, error) {
	provider.Warn(ctx, "read-warning")
	return r.Resource.Read(ctx, id)
}

func (r warnRes) Create(ctx context.Context, d provider.Attributes) (string, provider.Attributes, error) {
	provider.Warn(ctx, "create-warning")
	return r.Resource.Create(ctx, d)
}

func TestProviderWarningsSurface(t *testing.T) {
	p := warnProvider{mock.New()}
	old := newProvider
	newProvider = func() provider.Provider { return p }
	t.Cleanup(func() { newProvider = old })
	dir := t.TempDir()
	cfg, st := filepath.Join(dir, "baseplate.bp.hcl"), filepath.Join(dir, "s.bpstate")
	writePDCfg(t, cfg, `resource "mock_thing" "a" { name = "a" }`)

	var out bytes.Buffer
	if code := runOut([]string{"apply", "--auto-approve", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("apply exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Warning: create-warning") {
		t.Fatalf("apply must print an apply-time provider warning:\n%s", out.String())
	}

	out.Reset()
	if code := runOut([]string{"plan", "--json", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("plan exit %d\n%s", code, out.String())
	}
	var res struct {
		Result struct {
			Warnings []string `json:"warnings"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(res.Result.Warnings) != 1 || res.Result.Warnings[0] != "read-warning" {
		t.Fatalf("plan --json warnings = %q, want the Read warning\n%s", res.Result.Warnings, out.String())
	}

	out.Reset()
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("plan exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Warning: read-warning") {
		t.Fatalf("plan text must print the provider warning:\n%s", out.String())
	}

	// A converged apply (nothing to change) still reports the warning once.
	out.Reset()
	if code := runOut([]string{"apply", "--auto-approve", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("apply exit %d\n%s", code, out.String())
	}
	if strings.Count(out.String(), "Warning: read-warning") != 1 {
		t.Fatalf("want the warning once:\n%s", out.String())
	}
}
