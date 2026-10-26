package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

func replaceCfg(rev, lifecycle string) string {
	return `
resource "mock_replace" "a" {
  name = "a"
  rev  = "` + rev + `"
` + lifecycle + `
}
resource "mock_ref" "b" {
  name   = "b"
  target = mock_replace.a.id
}
`
}

func stateID(t *testing.T, st, address string) string {
	t.Helper()
	s, err := backend.NewLocal(st).Get()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := s.Find(address)
	if !ok {
		t.Fatalf("%s not in state", address)
	}
	return r.ID
}

func TestPlanJSONReportsReplaceAndDependentUpdate(t *testing.T) {
	cfg, st := pdSetup(t, replaceCfg("1", ""))
	writePDCfg(t, cfg, replaceCfg("2", ""))

	var out bytes.Buffer
	if code := runOut([]string{"plan", "--json", "--config", cfg, "--state", st}, &out); code != 2 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	var res struct {
		Changes []struct {
			Address           string   `json:"address"`
			Action            string   `json:"action"`
			ForcesReplacement []string `json:"forces_replacement"`
		} `json:"changes"`
		Summary struct{ Add, Change, Destroy, Replace int } `json:"summary"`
		Warnings []string                                   `json:"warnings"`
	}
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := map[string]string{}
	for _, c := range res.Changes {
		got[c.Address] = c.Action
		if c.Address == "mock_replace.a" && (len(c.ForcesReplacement) != 1 || c.ForcesReplacement[0] != "rev") {
			t.Fatalf("forces_replacement = %v", c.ForcesReplacement)
		}
	}
	if got["mock_replace.a"] != "replace" || got["mock_ref.b"] != "update" {
		t.Fatalf("actions = %v", got)
	}
	if res.Summary.Add != 1 || res.Summary.Destroy != 1 || res.Summary.Change != 1 || res.Summary.Replace != 1 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("a replace must not warn: %v", res.Warnings)
	}

	out.Reset()
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 2 ||
		!strings.Contains(out.String(), "-/+ mock_replace.a (replace)") ||
		!strings.Contains(out.String(), "rev: 1 => 2  # forces replacement") {
		t.Fatalf("text plan exit %d:\n%s", code, out.String())
	}
}

// --target on the replaced resource alone leaves the dependent for the next
// run, which then updates it to the new id and converges.
func TestTargetedReplaceThenDependentCatchesUp(t *testing.T) {
	cfg, st := pdSetup(t, replaceCfg("1", ""))
	oldID := stateID(t, st, "mock_replace.a")
	writePDCfg(t, cfg, replaceCfg("2", ""))

	var out bytes.Buffer
	if code := runOut([]string{"apply", "--target", "mock_replace.a", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("targeted apply exit %d\n%s", code, out.String())
	}
	newID := stateID(t, st, "mock_replace.a")
	if newID == oldID {
		t.Fatal("targeted replace must record a new id")
	}
	out.Reset()
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 2 ||
		!strings.Contains(out.String(), "~ mock_ref.b (update)") || strings.Contains(out.String(), "mock_replace.a (replace)") {
		t.Fatalf("follow-up plan exit %d:\n%s", code, out.String())
	}
	out.Reset()
	if code := runOut([]string{"apply", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("apply exit %d\n%s", code, out.String())
	}
	out.Reset()
	if code := runOut([]string{"plan", "--config", cfg, "--state", st}, &out); code != 0 {
		t.Fatalf("must converge, exit %d\n%s", code, out.String())
	}
}

func TestPreventDestroyBlocksReplaceInCLI(t *testing.T) {
	lc := "  lifecycle {\n    prevent_destroy = true\n  }"
	cfg, st := pdSetup(t, replaceCfg("1", lc))
	oldID := stateID(t, st, "mock_replace.a")
	writePDCfg(t, cfg, replaceCfg("2", lc))
	var out bytes.Buffer
	if code := runOut([]string{"apply", "--config", cfg, "--state", st}, &out); code != 1 ||
		!strings.Contains(out.String(), "mock_replace.a has lifecycle.prevent_destroy set, but the plan calls for this resource to be replaced") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if stateID(t, st, "mock_replace.a") != oldID {
		t.Fatal("a blocked replace must not change anything")
	}
	// Targeting only the dependent sidesteps the protected replace.
	out.Reset()
	if code := runOut([]string{"plan", "--target", "mock_ref.b", "--config", cfg, "--state", st}, &out); code == 1 {
		t.Fatalf("targeting another resource must not be blocked:\n%s", out.String())
	}
}
