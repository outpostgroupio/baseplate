package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// runOut drives run() with an empty stdin, a non-TTY (CI-like) terminal, and a
// single buffer capturing stdout; errors go to the same buffer.
func runOut(args []string, out *bytes.Buffer) int {
	return run(args, streams{
		in:    strings.NewReader(""),
		out:   out,
		err:   out,
		isTTY: func() bool { return false },
	})
}

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
	if err := importRun(p, cfg, statePath, "mock_thing.a", "mock_thing/9", output.Text, streams{
		in: strings.NewReader(""), out: &out, err: &out, isTTY: func() bool { return false },
	}); err != nil {
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
	code := runOut([]string{"plan", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
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
	code := runOut([]string{"import", "mock_thing.ghost", "someid",
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
	code := runOut([]string{"import", "mock_thing.a", "missing",
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
	code := runOut([]string{"plan", "--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 {
		t.Fatalf("expected exit 1 (import id not found), got %d\n%s", code, out.String())
	}
}

func TestImportRequiresTwoArgs(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n"), 0o644)

	var out bytes.Buffer
	code := runOut([]string{"import", "mock_thing.a", "--config", cfg,
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

func TestJSONErrorGoesToStderrEnvelope(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	// Malformed config -> config.Load fails -> structured error under --json.
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name =\n"), 0o644)

	var out, errBuf bytes.Buffer
	code := run([]string{"validate", "--json", "--config", cfg,
		"--state", filepath.Join(dir, "s.bpstate")}, streams{
		in: strings.NewReader(""), out: &out, err: &errBuf, isTTY: func() bool { return false },
	})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", out.String())
	}
	if !strings.Contains(errBuf.String(), `"format_version": 1`) || !strings.Contains(errBuf.String(), `"error"`) {
		t.Fatalf("stderr should carry the JSON error envelope, got %q", errBuf.String())
	}
}

func TestPlanJSONHasEnvelopeAndSummary(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n"), 0o644)

	var out bytes.Buffer
	code := runOut([]string{"plan", "--json", "--config", cfg,
		"--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 2 {
		t.Fatalf("expected exit 2 (changes), got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"format_version": 1`) ||
		!strings.Contains(out.String(), `"command": "plan"`) ||
		!strings.Contains(out.String(), `"summary"`) {
		t.Fatalf("plan --json missing envelope/summary: %s", out.String())
	}
}

// applyStreams builds streams with a scripted stdin and a chosen TTY-ness.
func applyStreams(stdin string, tty bool, out *bytes.Buffer) streams {
	return streams{in: strings.NewReader(stdin), out: out, err: out, isTTY: func() bool { return tty }}
}

func writeCfg(t *testing.T, dir string) (cfg, statePath string) {
	t.Helper()
	cfg = filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n"), 0o644)
	return cfg, filepath.Join(dir, "s.bpstate")
}

func TestApplyPromptAcceptsYes(t *testing.T) {
	dir := t.TempDir()
	cfg, st := writeCfg(t, dir)
	var out bytes.Buffer
	code := run([]string{"apply", "--config", cfg, "--state", st}, applyStreams("yes\n", true, &out))
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Apply complete") {
		t.Fatalf("apply should have run: %s", out.String())
	}
	if _, err := backend.NewLocal(st).Get(); err != nil {
		t.Fatalf("state should exist: %v", err)
	}
}

func TestApplyPromptCancelsOnNo(t *testing.T) {
	dir := t.TempDir()
	cfg, st := writeCfg(t, dir)
	var out bytes.Buffer
	code := run([]string{"apply", "--config", cfg, "--state", st}, applyStreams("no\n", true, &out))
	if code != 1 {
		t.Fatalf("expected exit 1 on cancel, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Apply cancelled") {
		t.Fatalf("expected cancellation message: %s", out.String())
	}
}

func TestApplyNonTTYAutoApplies(t *testing.T) {
	dir := t.TempDir()
	cfg, st := writeCfg(t, dir)
	var out bytes.Buffer
	// Empty stdin, non-TTY: must NOT block and must apply.
	code := run([]string{"apply", "--config", cfg, "--state", st}, applyStreams("", false, &out))
	if code != 0 || !strings.Contains(out.String(), "Apply complete") {
		t.Fatalf("non-TTY apply should proceed: code=%d out=%s", code, out.String())
	}
}

func TestApplyAutoApproveSkipsPrompt(t *testing.T) {
	dir := t.TempDir()
	cfg, st := writeCfg(t, dir)
	var out bytes.Buffer
	// TTY true but --auto-approve: must not read stdin, must apply.
	code := run([]string{"apply", "--auto-approve", "--config", cfg, "--state", st}, applyStreams("", true, &out))
	if code != 0 || !strings.Contains(out.String(), "Apply complete") {
		t.Fatalf("--auto-approve should apply: code=%d out=%s", code, out.String())
	}
}

func TestApplyJSONOnTTYRequiresAutoApprove(t *testing.T) {
	dir := t.TempDir()
	cfg, st := writeCfg(t, dir)
	var out, errBuf bytes.Buffer
	code := run([]string{"apply", "--json", "--config", cfg, "--state", st}, streams{
		in: strings.NewReader("yes\n"), out: &out, err: &errBuf, isTTY: func() bool { return true },
	})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(errBuf.String(), "requires --auto-approve") {
		t.Fatalf("expected auto-approve guidance on stderr: %s", errBuf.String())
	}
}

func TestApplyJSONResultEnvelope(t *testing.T) {
	dir := t.TempDir()
	cfg, st := writeCfg(t, dir)
	var out bytes.Buffer
	code := run([]string{"apply", "--json", "--auto-approve", "--config", cfg, "--state", st}, applyStreams("", false, &out))
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"command": "apply"`) || !strings.Contains(out.String(), `"applied"`) {
		t.Fatalf("apply --json envelope wrong: %s", out.String())
	}
}

func TestValidateJSONResult(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte("resource \"mock_thing\" \"a\" {\n  name = \"root\"\n}\n"), 0o644)

	var out bytes.Buffer
	code := runOut([]string{"validate", "--json", "--config", cfg,
		"--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"valid": true`) {
		t.Fatalf("validate --json wrong: %s", out.String())
	}
}

func TestVersionCommandPrintsBanner(t *testing.T) {
	var out bytes.Buffer
	if code := runOut([]string{"version"}, &out); code != 0 {
		t.Fatalf("run(version) exit = %d, want 0", code)
	}
	if got := out.String(); !strings.Contains(got, "baseplate ") || !strings.Contains(got, "commit") {
		t.Fatalf("version output = %q, want the version banner", got)
	}
}

func TestOpenBackendLocalWhenNoBlock(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(cfg, []byte(`resource "mock_thing" "a" { name = "x" }`), 0o644)
	b, _, err := openBackend(cfg, filepath.Join(dir, "s.bpstate"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.(*backend.Local); !ok {
		t.Fatalf("want *backend.Local, got %T", b)
	}
}

func TestOpenBackendHTTPForHTTPBlock(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(cfg, []byte("backend \"http\" { address = \"https://x/state\" }\n"), 0o644)
	b, _, err := openBackend(cfg, filepath.Join(dir, "s.bpstate"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.(*backend.HTTP); !ok {
		t.Fatalf("want *backend.HTTP, got %T", b)
	}
}

func TestOpenBackendUnknownType(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(cfg, []byte("backend \"s3\" { bucket = \"b\" }\n"), 0o644)
	if _, _, err := openBackend(cfg, filepath.Join(dir, "s.bpstate")); err == nil {
		t.Fatal("expected error for unknown backend type")
	}
}

func TestOpenBackendRejectsUnknownAttr(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(cfg, []byte("backend \"http\" { address = \"a\"\n adress = \"typo\" }\n"), 0o644)
	if _, _, err := openBackend(cfg, filepath.Join(dir, "s.bpstate")); err == nil {
		t.Fatal("expected error for unknown backend attribute")
	}
}

// The destroy safety note (spec I3, refined): a destroy that falls back to the
// local backend because NO config file was found warns on stderr, so a
// remote-state user who forgot --config isn't misled by a silent "destroyed
// nothing". A config that merely lacks a backend block stays quiet (below).
func TestDestroyNoConfigPrintsNote(t *testing.T) {
	dir := t.TempDir()
	st := filepath.Join(dir, "s.bpstate")
	var out, errBuf bytes.Buffer
	// --config points at a nonexistent file.
	code := run([]string{"destroy", "--auto-approve", "--config", filepath.Join(dir, "nope.bp.hcl"), "--state", st}, streams{
		in: strings.NewReader(""), out: &out, err: &errBuf, isTTY: func() bool { return false },
	})
	if code != 0 {
		t.Fatalf("destroy exit = %d, want 0\n%s", code, out.String()+errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "using local state backend") {
		t.Fatalf("expected local-fallback note on stderr, got %q", errBuf.String())
	}
}

// A destroy with a config file present (but no backend block) is a deliberate
// local setup and must NOT print the note (avoids per-destroy noise).
func TestDestroyWithConfigNoNote(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte(`resource "mock_thing" "a" { name = "x" }`), 0o644)
	st := filepath.Join(dir, "s.bpstate")
	var out, errBuf bytes.Buffer
	code := run([]string{"destroy", "--auto-approve", "--config", cfg, "--state", st}, streams{
		in: strings.NewReader(""), out: &out, err: &errBuf, isTTY: func() bool { return false },
	})
	if code != 0 {
		t.Fatalf("destroy exit = %d, want 0\n%s", code, out.String()+errBuf.String())
	}
	if strings.Contains(errBuf.String(), "using local state backend") {
		t.Fatalf("no note expected when a config file exists, got %q", errBuf.String())
	}
}
