package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

const keyedCfg = `
resource "mock_thing" "set" {
  for_each = toset(["a", "b"])
  name     = each.key
}
resource "mock_thing" "solo" {
  name = "solo"
}
`

func writeKeyedCfg(t *testing.T) (dir, cfg, st string) {
	t.Helper()
	dir = t.TempDir()
	cfg = filepath.Join(dir, "baseplate.bp.hcl")
	if err := os.WriteFile(cfg, []byte(keyedCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, cfg, filepath.Join(dir, "s.bpstate")
}

func TestTargetInstanceVsResourceWide(t *testing.T) {
	changes := func() *plan.Plan {
		return &plan.Plan{Changes: []plan.Change{
			{Address: `mock_thing.set["a"]`, Type: "mock_thing", Name: "set", Key: "a", Action: provider.ActionCreate},
			{Address: `mock_thing.set["b"]`, Type: "mock_thing", Name: "set", Key: "b", Action: provider.ActionCreate},
			{Address: "mock_thing.solo", Type: "mock_thing", Name: "solo", Action: provider.ActionCreate},
		}}
	}
	cases := map[string][]string{
		`mock_thing.set`:      {`mock_thing.set["a"]`, `mock_thing.set["b"]`},
		`mock_thing.set["b"]`: {`mock_thing.set["b"]`},
		`mock_thing.solo`:     {"mock_thing.solo"},
		`mock_thing.set["z"]`: nil,
	}
	for target, want := range cases {
		tgt, err := parseTarget(target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		pl := changes()
		filterPlanByTarget(pl, tgt)
		var got []string
		for _, c := range pl.Changes {
			got = append(got, c.Address)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("--target %s selected %v, want %v", target, got, want)
		}
	}
}

func TestApplyTargetInstanceAppliesOnlyThatInstance(t *testing.T) {
	_, cfg, st := writeKeyedCfg(t)
	var out bytes.Buffer
	code := runOut([]string{"apply", "--target", `mock_thing.set["b"]`, "--config", cfg, "--state", st}, &out)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	s, err := backend.NewLocal(st).Get()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Resources) != 1 || s.Resources[0].Address() != `mock_thing.set["b"]` {
		t.Fatalf("only instance b should be applied, state = %+v", s.Resources)
	}
}

func TestMalformedTargetIsError(t *testing.T) {
	_, cfg, st := writeKeyedCfg(t)
	for _, cmd := range []string{"plan", "apply", "destroy"} {
		var out bytes.Buffer
		code := runOut([]string{cmd, "--target", "mock_thing.set[a]", "--config", cfg, "--state", st}, &out)
		if code != 1 {
			t.Fatalf("%s: malformed --target must exit 1, got %d\n%s", cmd, code, out.String())
		}
		if !strings.Contains(out.String(), `--target: invalid address "mock_thing.set[a]": key must be a quoted string`) {
			t.Fatalf("%s: unexpected error output: %s", cmd, out.String())
		}
	}
}

func TestImportKeyedInstanceAddress(t *testing.T) {
	_, cfg, st := writeKeyedCfg(t)
	p := mock.New()
	p.Seed("mock_thing/9", provider.Attributes{"name": cty.StringVal("a")})
	var out bytes.Buffer
	if err := importRun(p, cfg, st, `mock_thing.set["a"]`, "mock_thing/9", output.Text, applyStreams("", false, &out)); err != nil {
		t.Fatal(err)
	}
	s, _ := backend.NewLocal(st).Get()
	r, ok := s.Find(`mock_thing.set["a"]`)
	if !ok || r.Key != "a" || r.ID != "mock_thing/9" {
		t.Fatalf("import should record the keyed instance, got %+v", s.Resources)
	}
}

func TestImportRejectsMalformedAndUnindexedAddress(t *testing.T) {
	_, cfg, st := writeKeyedCfg(t)
	p := mock.New()
	var out bytes.Buffer
	err := importRun(p, cfg, st, "mock_thing.set[a]", "x", output.Text, applyStreams("", false, &out))
	if err == nil || !strings.Contains(err.Error(), "key must be a quoted string") {
		t.Fatalf("malformed import address: %v", err)
	}
	err = importRun(p, cfg, st, "mock_thing.set", "x", output.Text, applyStreams("", false, &out))
	if err == nil || !strings.Contains(err.Error(), "has for_each; import a specific instance") {
		t.Fatalf("unindexed import of a for_each resource: %v", err)
	}
}

func TestGenerateConfigRejectsForEachImport(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte(`
import {
  for_each = { a = "mock_thing/1" }
  to       = mock_thing.undeclared[each.key]
  id       = each.value
}
`), 0o644)
	var out bytes.Buffer
	code := runOut([]string{"plan", "--generate-config", filepath.Join(dir, "gen.bp.hcl"),
		"--config", cfg, "--state", filepath.Join(dir, "s.bpstate")}, &out)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "--generate-config does not support for_each resources; declare the resource block by hand") {
		t.Fatalf("expected the for_each guard, got %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "gen.bp.hcl")); !os.IsNotExist(err) {
		t.Fatal("no file should be generated")
	}
}

// writeShowState writes a deterministic state file for the show tests,
// including a sensitive attribute that must never be printed.
func writeShowState(t *testing.T, path string) {
	t.Helper()
	enc := func(v cty.Value) json.RawMessage {
		b, err := ctyjson.Marshal(v, cty.DynamicPseudoType)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	s := state.New()
	s.Serial, s.Lineage = 42, "00000000-0000-4000-8000-000000000000"
	s.Upsert(state.Resource{Type: "roblox_asset", Name: "sprites", Key: "ui/gem.png", ID: "1234567890",
		Attributes: map[string]json.RawMessage{
			"display_name": enc(cty.StringVal("sprite/ui/gem")),
			"asset_type":   enc(cty.StringVal("Image")),
		}})
	s.Upsert(state.Resource{Type: "mock_secret", Name: "api", ID: "mock_secret/1",
		Attributes: map[string]json.RawMessage{
			"name":  enc(cty.StringVal("API_KEY")),
			"value": enc(cty.StringVal("hunter2")),
		}})
	s.Upsert(state.Resource{Type: "mock_thing", Name: "solo", ID: "mock_thing/3",
		Attributes: map[string]json.RawMessage{
			"name":  enc(cty.StringVal("solo")),
			"count": enc(cty.NumberIntVal(3)),
		}})
	if err := backend.NewLocal(path).Put(s); err != nil {
		t.Fatal(err)
	}
}

func TestShowJSONGolden(t *testing.T) {
	dir := t.TempDir()
	st := filepath.Join(dir, "s.bpstate")
	writeShowState(t, st)
	var out bytes.Buffer
	code := runOut([]string{"show", "--json", "--config", filepath.Join(dir, "none.bp.hcl"), "--state", st}, &out)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	got := out.String()
	if strings.Contains(got, "hunter2") || strings.Contains(got, `"value"`) {
		t.Fatalf("show must filter sensitive attributes:\n%s", got)
	}
	golden := filepath.Join("testdata", "show.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		_ = os.MkdirAll("testdata", 0o755)
		_ = os.WriteFile(golden, []byte(got), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("show --json mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestShowText(t *testing.T) {
	dir := t.TempDir()
	st := filepath.Join(dir, "s.bpstate")
	writeShowState(t, st)
	cfg := filepath.Join(dir, "none.bp.hcl")

	var out bytes.Buffer
	if code := runOut([]string{"show", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	want := "mock_secret.api                     mock_secret/1\n" +
		"mock_thing.solo                     mock_thing/3\n" +
		"roblox_asset.sprites[\"ui/gem.png\"]  1234567890\n"
	if out.String() != want {
		t.Fatalf("show text:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()
	if code := runOut([]string{"show", `roblox_asset.sprites["ui/gem.png"]`, "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	for _, w := range []string{`roblox_asset.sprites["ui/gem.png"]`, "id           = 1234567890", "display_name = sprite/ui/gem"} {
		if !strings.Contains(out.String(), w) {
			t.Fatalf("show <address> missing %q:\n%s", w, out.String())
		}
	}

	out.Reset()
	if code := runOut([]string{"show", "mock_secret.api", "--config", cfg, "--state", st}, &out); code != 0 || strings.Contains(out.String(), "hunter2") {
		t.Fatalf("show <address> must filter sensitive values (exit %d):\n%s", code, out.String())
	}

	out.Reset()
	if code := runOut([]string{"show", "mock_thing.nope", "--config", cfg, "--state", st}, &out); code != 1 {
		t.Fatalf("show of an address not in state must exit 1, got %d", code)
	}
}

func TestShowWithNoState(t *testing.T) {
	dir := t.TempDir()
	args := []string{"--config", filepath.Join(dir, "none.bp.hcl"), "--state", filepath.Join(dir, "missing.bpstate")}
	var out bytes.Buffer
	if code := runOut(append([]string{"show"}, args...), &out); code != 0 || out.String() != "No resources in state.\n" {
		t.Fatalf("show with no state: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := runOut(append([]string{"show", "--json"}, args...), &out); code != 0 || !strings.Contains(out.String(), `"resources": []`) {
		t.Fatalf("show --json with no state: exit %d, %s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.bpstate")); !os.IsNotExist(err) {
		t.Fatal("show must not create a state file")
	}
}

func TestPlanWarnsWhenForEachEmptiesState(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte(`
resource "mock_thing" "set" {
  for_each = fileset("${path.config}", "*.png")
  name     = each.key
}
`), 0o644)
	st := filepath.Join(dir, "s.bpstate")
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "a.png", ID: "mock_thing/1"})
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "b.png", ID: "mock_thing/2"})
	if err := backend.NewLocal(st).Put(s); err != nil {
		t.Fatal(err)
	}
	const want = "for_each for mock_thing.set is empty; all 2 instances will be destroyed."

	var out bytes.Buffer
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 2 || !strings.Contains(out.String(), "Warning: "+want) {
		t.Fatalf("text plan (exit %d) missing warning:\n%s", code, out.String())
	}
	out.Reset()
	if code := runOut([]string{"plan", "--json", "--config", cfg, "--state", st}, &out); code != 2 {
		t.Fatalf("plan --json exit %d\n%s", code, out.String())
	}
	var env struct {
		Result struct {
			Warnings []string `json:"warnings"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Result.Warnings) != 1 || env.Result.Warnings[0] != want {
		t.Fatalf("plan --json warnings = %q", env.Result.Warnings)
	}
}
