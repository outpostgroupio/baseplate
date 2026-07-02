package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
