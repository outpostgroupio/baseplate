package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

const robloxCfg = `
resource "roblox_experience" "main" {
  universe_id = "1"
  vr_enabled  = false
}
`

func TestValidateWorksWithoutAPIKey(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte(robloxCfg), 0o644)

	var out bytes.Buffer
	if code := runOut([]string{"validate", "--config", cfg}, &out); code != 0 {
		t.Fatalf("validate must not need ROBLOX_API_KEY, exit %d:\n%s", code, out.String())
	}
}

func TestValidateWithoutAPIKeyStillChecksAttributes(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"roblox_experience\" \"main\" {\n  universe_id = \"1\"\n  not_an_attr = true\n}\n"), 0o644)

	var out bytes.Buffer
	if code := runOut([]string{"validate", "--config", cfg}, &out); code != 1 || !strings.Contains(out.String(), "not_an_attr") {
		t.Fatalf("validate must reject an unknown attribute, exit %d:\n%s", code, out.String())
	}
}

func TestPlanWithoutAPIKeyNamesTheKey(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte(robloxCfg), 0o644)

	var out bytes.Buffer
	code := runOut([]string{"plan", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 || !strings.Contains(out.String(), "ROBLOX_API_KEY is not set") {
		t.Fatalf("plan without a key must say so, exit %d:\n%s", code, out.String())
	}
}

func TestEveryCommandHasAShortDescription(t *testing.T) {
	var out bytes.Buffer
	runOut([]string{"--help"}, &out)
	for _, name := range []string{"apply", "destroy", "import", "plan", "show", "validate", "version"} {
		for _, line := range strings.Split(out.String(), "\n") {
			f := strings.Fields(line)
			if len(f) == 1 && f[0] == name {
				t.Fatalf("%s has no short description in --help:\n%s", name, out.String())
			}
		}
	}
}

func TestImportWithNoArgsRunsImportBlocks(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("a")})
	p.Seed("mock_thing/2", provider.Attributes{"name": cty.StringVal("b")})
	old := newProvider
	newProvider = func() provider.Provider { return p }
	t.Cleanup(func() { newProvider = old })

	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	statePath := filepath.Join(dir, "s.bpstate")
	os.WriteFile(cfg, []byte(`
locals {
  ids = { a = "mock_thing/1", b = "mock_thing/2" }
}

resource "mock_thing" "set" {
  for_each = local.ids
  name     = each.key
}

import {
  for_each = local.ids
  to       = mock_thing.set[each.key]
  id       = each.value
}
`), 0o644)

	var out bytes.Buffer
	if code := runOut([]string{"import", "--config", cfg, "--state", statePath}, &out); code != 0 {
		t.Fatalf("import with no args failed, exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Imported 2 resource(s)") {
		t.Fatalf("want both blocks imported:\n%s", out.String())
	}
	st, err := backend.NewLocal(statePath).Get()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Resources) != 2 {
		t.Fatalf("want 2 resources in state, got %+v", st.Resources)
	}
	if p.Creates() != 0 {
		t.Fatal("import must never create remote objects")
	}

	out.Reset()
	if code := runOut([]string{"plan", "--config", cfg, "--state", statePath}, &out); code != 0 {
		t.Fatalf("plan after import must be clean (exit 0), got %d:\n%s", code, out.String())
	}
	out.Reset()
	if code := runOut([]string{"import", "--config", cfg, "--state", statePath}, &out); code != 0 || !strings.Contains(out.String(), "Nothing to import") {
		t.Fatalf("a second run must be a no-op, exit %d:\n%s", code, out.String())
	}
}

func TestImportRejectsOneArg(t *testing.T) {
	var out bytes.Buffer
	if code := runOut([]string{"import", "mock_thing.a"}, &out); code != 1 || !strings.Contains(out.String(), "no arguments") {
		t.Fatalf("one argument must be rejected, exit %d:\n%s", code, out.String())
	}
}
