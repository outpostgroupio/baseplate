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

// sharedMock makes every run() in the test use one mock provider, so the
// remote objects an apply creates are there for the next command.
func sharedMock(t *testing.T) {
	t.Helper()
	p := mock.New()
	old := newProvider
	newProvider = func() provider.Provider { return p }
	t.Cleanup(func() { newProvider = old })
}

func mustRun(t *testing.T, wantCode int, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if code := runOut(args, &out); code != wantCode {
		t.Fatalf("%v: exit %d, want %d\n%s", args, code, wantCode, out.String())
	}
	return out.String()
}

// An output block's value is planned (unknown until its resource exists),
// recorded by apply, printed by `baseplate output`, re-planned as a change
// when it changes, removed with its block, and removed by destroy.
func TestOutputsLifecycle(t *testing.T) {
	sharedMock(t)
	dir := t.TempDir()
	cfg, st := filepath.Join(dir, "c.bp.hcl"), filepath.Join(dir, "s.bpstate")
	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(cfg, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := `
resource "mock_thing" "a" { name = "root" }
resource "mock_thing" "set" {
  for_each = toset(["x", "y"])
  name     = each.key
}
`
	write(base + `
output "a_id" { value = mock_thing.a.id }
output "names" { value = [for k, v in mock_thing.set : v.name] }
output "secret" {
  value     = "s3cret"
  sensitive = true
}
`)
	out := mustRun(t, 2, "plan", "--config", cfg, "--state", st)
	for _, want := range []string{"Changes to Outputs:", "+ a_id = (known after apply)", "+ secret = (sensitive value)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan missing %q:\n%s", want, out)
		}
	}
	out = mustRun(t, 0, "apply", "--auto-approve", "--config", cfg, "--state", st)
	if !strings.Contains(out, "Outputs:\n\na_id = \"mock_thing/") || !strings.Contains(out, `names = ["x", "y"]`) || !strings.Contains(out, "secret = <sensitive>") {
		t.Fatalf("apply should list the outputs:\n%s", out)
	}
	s, err := backend.NewLocal(st).Get()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Find("mock_thing.a")
	if got, _ := s.Outputs["a_id"].CtyValue(); got.AsString() != a.ID || !s.Outputs["secret"].Sensitive {
		t.Fatalf("outputs in state = %+v", s.Outputs)
	}

	if out := mustRun(t, 0, "plan", "--config", cfg, "--state", st); strings.Contains(out, "Outputs") {
		t.Fatalf("re-plan should have no output changes:\n%s", out)
	}
	if out := mustRun(t, 0, "output", "--config", cfg, "--state", st); !strings.Contains(out, "secret = <sensitive>") || !strings.Contains(out, "a_id = ") {
		t.Fatalf("output:\n%s", out)
	}
	if out := mustRun(t, 0, "output", "secret", "--raw", "--config", cfg, "--state", st); out != "s3cret\n" {
		t.Fatalf("output secret --raw = %q", out)
	}
	out = mustRun(t, 0, "output", "names", "--json", "--config", cfg, "--state", st)
	var env struct {
		Result outputResult `json:"result"`
	}
	var compact bytes.Buffer
	if err := json.Unmarshal([]byte(out), &env); err != nil || json.Compact(&compact, env.Result.Value) != nil || compact.String() != `["x","y"]` {
		t.Fatalf("output names --json = %s (%v)", out, err)
	}
	mustRun(t, 1, "output", "nope", "--config", cfg, "--state", st)

	// A changed value and a removed block are output-only changes: plan
	// exits 2 and apply records them.
	write(base + `
output "a_id" { value = "fixed" }
`)
	out = mustRun(t, 2, "plan", "--config", cfg, "--state", st)
	if !strings.Contains(out, `~ a_id: "mock_thing/`) || !strings.Contains(out, `=> "fixed"`) || !strings.Contains(out, "- names = ") {
		t.Fatalf("plan should update a_id and remove names:\n%s", out)
	}
	mustRun(t, 0, "apply", "--auto-approve", "--config", cfg, "--state", st)
	s, _ = backend.NewLocal(st).Get()
	if len(s.Outputs) != 1 {
		t.Fatalf("outputs after apply = %+v", s.Outputs)
	}

	mustRun(t, 0, "destroy", "--auto-approve", "--config", cfg, "--state", st)
	s, _ = backend.NewLocal(st).Get()
	if s.Outputs != nil {
		t.Fatalf("destroy should remove the outputs, got %+v", s.Outputs)
	}
}

// An output that names an attribute the resource doesn't have fails apply
// once the resource exists (until then it is unknown), and then plan.
func TestOutputUnknownAttributeFails(t *testing.T) {
	sharedMock(t)
	dir := t.TempDir()
	cfg, st := filepath.Join(dir, "c.bp.hcl"), filepath.Join(dir, "s.bpstate")
	os.WriteFile(cfg, []byte(`
resource "mock_thing" "a" { name = "root" }
output "bad" { value = mock_thing.a.nope }
`), 0o644)
	if out := mustRun(t, 1, "apply", "--auto-approve", "--config", cfg, "--state", st); !strings.Contains(out, "apply succeeded, but evaluating the outputs failed") {
		t.Fatalf("apply error:\n%s", out)
	}
	if out := mustRun(t, 1, "plan", "--config", cfg, "--state", st); !strings.Contains(out, `output "bad"`) {
		t.Fatalf("plan error should name the output:\n%s", out)
	}
}
