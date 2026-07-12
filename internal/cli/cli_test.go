package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/mock"
	"github.com/outpostgroup/baseplate/internal/state/backend"
)

// TestImportRunWritesStateAndReports is the CLI happy path: importRun takes an
// injectable provider, so we seed a mock "remote" resource and assert import
// records it in the state file and prints the success line. (The full command
// path can't be tested against a seeded remote — run() builds its own provider —
// so we drive importRun directly, which is what the command calls.)
func TestImportRunWritesStateAndReports(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n"), 0o644)
	statePath := filepath.Join(dir, "s.bpstate")

	p := mock.New()
	p.Seed("mock_thing/9", provider.Attributes{"name": cty.StringVal("root")})

	var out bytes.Buffer
	if err := importRun(p, cfg, statePath, "mock_thing.a", "mock_thing/9", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Imported mock_thing.a (id mock_thing/9)") {
		t.Fatalf("expected a success line, got %q", out.String())
	}
	s, err := backend.NewLocal(statePath).Get()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Find("mock_thing.a"); !ok || got.ID != "mock_thing/9" {
		t.Fatalf("state file should contain the imported resource, got %+v", got)
	}
}

func TestPlanExitsWithChangesCode(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte(`
resource "mock_thing" "a" {
  name = "root"
}
`), 0o644)

	var out bytes.Buffer
	code := run([]string{"plan", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 2 {
		t.Fatalf("expected exit 2 (changes present), got %d\n%s", code, out.String())
	}
}

// TestFilterPlanByTargetDropsUntargetedDeletes guards the --target footgun:
// a targeted operation must restrict the plan to that address and must NOT
// delete other (untargeted) resources that happen to be in state.
func TestFilterPlanByTargetDropsUntargetedDeletes(t *testing.T) {
	pl := &plan.Plan{Changes: []plan.Change{
		{Address: "mock_thing.other", Action: provider.ActionDelete},
		{Address: "mock_thing.target", Action: provider.ActionCreate},
	}}
	filterPlanByTarget(pl, "mock_thing.target")
	if len(pl.Changes) != 1 || pl.Changes[0].Address != "mock_thing.target" {
		t.Fatalf("target must restrict plan to itself and not delete other resources; got %+v", pl.Changes)
	}
	if pl.Changes[0].Action == provider.ActionDelete {
		t.Fatal("targeted change should not be a delete of another resource")
	}
}

func TestImportErrorsWhenAddressNotInConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n"), 0o644)

	var out bytes.Buffer
	code := run([]string{"import", "mock_thing.ghost", "someid",
		"--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 {
		t.Fatalf("expected exit 1 for an undeclared address, got %d\n%s", code, out.String())
	}
}

func TestImportErrorsWhenRemoteNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n"), 0o644)

	// No ROBLOX_API_KEY → the mock provider with an empty store → Read → ErrNotFound.
	var out bytes.Buffer
	code := run([]string{"import", "mock_thing.a", "missing",
		"--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 {
		t.Fatalf("expected exit 1 for a not-found remote, got %d\n%s", code, out.String())
	}
}

func TestPlanWithImportBlockNotFoundExits1(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\nimport {\n  to = mock_thing.a\n  id = \"missing\"\n}\n"), 0o644)

	var out bytes.Buffer
	// The import target isn't in the (empty) mock store → ResolveImports Read → not found → exit 1.
	code := run([]string{"plan", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 {
		t.Fatalf("expected exit 1 (import id not found), got %d\n%s", code, out.String())
	}
}

func TestImportRequiresTwoArgs(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n"), 0o644)

	var out bytes.Buffer
	code := run([]string{"import", "mock_thing.a", "--config", cfg,
		"--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 {
		t.Fatalf("expected exit 1 when id arg is missing, got %d\n%s", code, out.String())
	}
}

func TestGenerateConfigWritesFileForUndeclaredImport(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "gen.bp.hcl")
	p := mock.New()
	p.Seed("mock_thing/9", provider.Attributes{"name": cty.StringVal("root")})

	imports := []plan.ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/9"}}
	declared := map[string]bool{} // mock_thing.a is NOT declared → generate it

	var outbuf bytes.Buffer
	if err := generateConfig(context.Background(), p, imports, declared, out, &outbuf); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `resource "mock_thing" "a"`) || !strings.Contains(string(data), `name = "root"`) {
		t.Fatalf("generated file wrong:\n%s", data)
	}
	if !strings.Contains(outbuf.String(), "Generated configuration for 1 resource(s)") {
		t.Fatalf("missing report line: %q", outbuf.String())
	}
}

func TestGenerateConfigSkipsDeclaredImports(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "gen.bp.hcl")
	p := mock.New()
	imports := []plan.ImportBlock{{Address: "mock_thing.a", ID: "mock_thing/9"}}
	declared := map[string]bool{"mock_thing.a": true} // declared → not generated

	var outbuf bytes.Buffer
	if err := generateConfig(context.Background(), p, imports, declared, out, &outbuf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("no file should be written when every import is declared")
	}
}

func TestGenerateConfigRejectsDuplicateUndeclaredImports(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "gen.bp.hcl")
	p := mock.New()
	p.Seed("mock_thing/9", provider.Attributes{"name": cty.StringVal("root")})
	imports := []plan.ImportBlock{
		{Address: "mock_thing.a", ID: "mock_thing/9"},
		{Address: "mock_thing.a", ID: "mock_thing/9"},
	}
	var outbuf bytes.Buffer
	err := generateConfig(context.Background(), p, imports, map[string]bool{}, out, &outbuf)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected a duplicate error, got %v", err)
	}
}
