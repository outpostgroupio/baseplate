package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

const protectedCfg = `
resource "mock_thing" "prod" {
  name = "prod"
  lifecycle {
    prevent_destroy = true
  }
}
resource "mock_thing" "scratch" {
  name = "scratch"
}
`

const pdError = "Instance cannot be destroyed: mock_thing.prod has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed. To avoid this error and continue with the plan, either disable lifecycle.prevent_destroy or reduce the scope of the plan using the --target flag."

// pdSetup applies src with a shared mock provider (so later runs see the same
// remote objects) and returns the config and state paths.
func pdSetup(t *testing.T, src string) (cfg, st string) {
	t.Helper()
	p := mock.New()
	old := newProvider
	newProvider = func() provider.Provider { return p }
	t.Cleanup(func() { newProvider = old })

	dir := t.TempDir()
	cfg = filepath.Join(dir, "baseplate.bp.hcl")
	st = filepath.Join(dir, "s.bpstate")
	writePDCfg(t, cfg, src)
	var out bytes.Buffer
	if code := runOut([]string{"apply", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("setup apply exit %d\n%s", code, out.String())
	}
	return cfg, st
}

func writePDCfg(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stateAddrs(t *testing.T, st string) []string {
	t.Helper()
	s, err := backend.NewLocal(st).Get()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range s.Resources {
		out = append(out, r.Address())
	}
	return out
}

func TestDestroyBlockedByPreventDestroy(t *testing.T) {
	cfg, st := pdSetup(t, protectedCfg)
	var out bytes.Buffer
	code := runOut([]string{"destroy", "--config", cfg, "--state", st}, &out)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Error: "+pdError) {
		t.Fatalf("missing Terraform-style error:\n%s", out.String())
	}
	if got := strings.Join(stateAddrs(t, st), ","); got != "mock_thing.prod,mock_thing.scratch" {
		t.Fatalf("a blocked destroy must not delete anything; state = %s", got)
	}
}

func TestDestroyPreventDestroyJSONEnvelope(t *testing.T) {
	cfg, st := pdSetup(t, protectedCfg)
	var out, errBuf bytes.Buffer
	code := run([]string{"destroy", "--json", "--auto-approve", "--config", cfg, "--state", st}, streams{
		in: strings.NewReader(""), out: &out, err: &errBuf, isTTY: func() bool { return false },
	})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", out.String())
	}
	var env struct {
		FormatVersion int    `json:"format_version"`
		Command       string `json:"command"`
		Error         struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errBuf.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not a JSON envelope: %v\n%s", err, errBuf.String())
	}
	if env.FormatVersion != 1 || env.Command != "destroy" || env.Error.Message != pdError {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

func TestPlanBlockedByRemovedForEachKey(t *testing.T) {
	cfg, st := pdSetup(t, `
resource "mock_thing" "env" {
  for_each = toset(["prod", "staging"])
  name     = each.key
  lifecycle { prevent_destroy = true }
}
`)
	writePDCfg(t, cfg, `
resource "mock_thing" "env" {
  for_each = toset(["prod"])
  name     = each.key
  lifecycle { prevent_destroy = true }
}
`)
	for _, cmd := range []string{"plan", "apply"} {
		var out bytes.Buffer
		code := runOut([]string{cmd, "--config", cfg, "--state", st}, &out)
		if code != 1 || !strings.Contains(out.String(), `Instance cannot be destroyed: mock_thing.env["staging"] has lifecycle.prevent_destroy set`) {
			t.Fatalf("%s: expected the prevent_destroy error (exit 1), got %d\n%s", cmd, code, out.String())
		}
	}
	if got := len(stateAddrs(t, st)); got != 2 {
		t.Fatalf("blocked apply must not delete; %d instance(s) in state", got)
	}
}

func TestPlanBlockedByEmptyForEach(t *testing.T) {
	cfg, st := pdSetup(t, `
resource "mock_thing" "env" {
  for_each = toset(["prod"])
  name     = each.key
  lifecycle { prevent_destroy = true }
}
`)
	writePDCfg(t, cfg, `
resource "mock_thing" "env" {
  for_each = toset([])
  name     = each.key
  lifecycle { prevent_destroy = true }
}
`)
	var out bytes.Buffer
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 1 ||
		!strings.Contains(out.String(), `mock_thing.env["prod"] has lifecycle.prevent_destroy set`) {
		t.Fatalf("expected the prevent_destroy error, got %d\n%s", code, out.String())
	}
}

func TestDestroyTargetOutsideProtectedResourceProceeds(t *testing.T) {
	cfg, st := pdSetup(t, protectedCfg)
	var out bytes.Buffer
	code := runOut([]string{"destroy", "--target", "mock_thing.scratch", "--config", cfg, "--state", st}, &out)
	if code != 0 {
		t.Fatalf("targeted destroy of an unprotected resource should succeed, got %d\n%s", code, out.String())
	}
	if got := strings.Join(stateAddrs(t, st), ","); got != "mock_thing.prod" {
		t.Fatalf("only scratch should be destroyed; state = %s", got)
	}
	// Targeting the protected resource itself is still blocked.
	out.Reset()
	if code := runOut([]string{"destroy", "--target", "mock_thing.prod", "--config", cfg, "--state", st}, &out); code != 1 ||
		!strings.Contains(out.String(), pdError) {
		t.Fatalf("targeting the protected resource must fail, got %d\n%s", code, out.String())
	}
}

func TestRemovingLifecycleLiftsProtection(t *testing.T) {
	cfg, st := pdSetup(t, protectedCfg)
	writePDCfg(t, cfg, `
resource "mock_thing" "prod" {
  name = "prod"
}
resource "mock_thing" "scratch" {
  name = "scratch"
}
`)
	var out bytes.Buffer
	if code := runOut([]string{"destroy", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("without lifecycle the destroy proceeds, got %d\n%s", code, out.String())
	}
	if got := stateAddrs(t, st); len(got) != 0 {
		t.Fatalf("everything should be destroyed; state = %v", got)
	}
}

func TestRemovingResourceBlockLiftsProtection(t *testing.T) {
	cfg, st := pdSetup(t, protectedCfg)
	// The protected block is gone entirely: the flag was never persisted to
	// state, so the delete goes ahead (Terraform behaves the same way).
	writePDCfg(t, cfg, `
resource "mock_thing" "scratch" {
  name = "scratch"
}
`)
	var out bytes.Buffer
	if code := runOut([]string{"apply", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("apply should delete the unprotected orphan, got %d\n%s", code, out.String())
	}
	if got := strings.Join(stateAddrs(t, st), ","); got != "mock_thing.scratch" {
		t.Fatalf("state = %s", got)
	}
}
