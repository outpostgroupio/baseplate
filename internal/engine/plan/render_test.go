package plan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
)

func TestRenderTextGolden(t *testing.T) {
	pl := &Plan{Changes: []Change{
		{Address: "mock_thing.a", Type: "mock_thing", Action: provider.ActionCreate,
			Desired: provider.Attributes{"name": cty.StringVal("root")}},
	}}
	got := pl.RenderText(false)
	golden := filepath.Join("..", "..", "..", "testdata", "plan_create.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		_ = os.WriteFile(golden, []byte(got), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("render mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
