package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
)

func TestPlanExitsWithChangesCode(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "baseplate.bp.hcl")
	os.WriteFile(cfg, []byte(`
resource "mock_thing" "a" {
  name = "root"
}
`), 0o644)

	var out bytes.Buffer
	code := run([]string{"plan", "--config", cfg, "--state", filepath.Join(dir, "s.tfstate")}, &out)
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
