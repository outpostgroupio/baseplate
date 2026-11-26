package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider/mock"
)

func TestOutputBlocks(t *testing.T) {
	head := `resource "mock_thing" "a" { name = "root" }
`
	p := filepath.Join(t.TempDir(), "c.bp.hcl")
	os.WriteFile(p, []byte(head+`
output "b" {
  value       = mock_thing.a.id
  description = "The id"
  sensitive   = true
}
output "a" { value = "x" }
`), 0o644)
	cfg, err := LoadConfig(p, mock.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outputs) != 2 || cfg.Outputs[0].Name != "a" || !cfg.Outputs[1].Sensitive || cfg.Outputs[1].Description != "The id" {
		t.Fatalf("outputs = %+v", cfg.Outputs)
	}
	vals, err := cfg.EvalOutputs(map[string]cty.Value{"mock_thing.a": cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("7")})})
	if err != nil || vals["b"].Value.AsString() != "7" || !vals["b"].Sensitive {
		t.Fatalf("EvalOutputs = %+v, %v", vals, err)
	}
	if vals, _ := cfg.EvalOutputs(nil); vals["b"].Value.IsKnown() {
		t.Fatal("an output of an instance not in state is unknown")
	}

	for src, want := range map[string]string{
		`output "a" { value = 1 }
output "a" { value = 2 }`: `duplicate output "a"`,
		`output "a" {}`: `"value" is required`,
		`output "a" {
  value     = 1
  sensitive = local.x
}`: "sensitive must be a literal",
		`output "a" { value = nope.x }`:   "Unknown variable",
		`output "a" { value = each.key }`: `"each" is only available`,
		`output "a" {
  value      = 1
  depends_on = []
}`: "Unsupported argument",
	} {
		wantErr(t, head+src, want)
	}
}
