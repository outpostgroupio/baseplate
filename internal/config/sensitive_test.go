package config

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
)

var secretOutputs = map[string]cty.Value{
	"token": cty.StringVal("s3cret").Mark(plan.SensitiveMark),
	"keys":  cty.SetVal([]cty.Value{cty.StringVal("a")}).Mark(plan.SensitiveMark),
	"plain": cty.StringVal("p"),
}

// A sensitive value makes every attribute computed from it sensitive, through
// locals, templates and lists; the value itself reaches the resource without
// the mark. nonsensitive() and sensitive() remove and add it.
func TestSensitiveValuesMarkAttributes(t *testing.T) {
	cfg, err := loadWith(t, assetsData+`
locals { token = data.baseplate_remote_state.assets.outputs.token }
resource "mock_ref" "r" {
  name   = "n-${local.token}"
  target = data.baseplate_remote_state.assets.outputs.plain
}
resource "mock_ref" "shown" {
  name   = nonsensitive(local.token)
  target = sensitive("literal")
}
`, stubReader(secretOutputs, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]plan.DesiredResource{}
	for _, d := range cfg.Resources {
		got[d.Address()] = d
	}
	r := got["mock_ref.r"]
	if strings.Join(r.SensitiveAttrs, ",") != "name" {
		t.Fatalf("SensitiveAttrs = %v, want [name]", r.SensitiveAttrs)
	}
	if v := r.Attributes["name"]; v.IsMarked() || v.AsString() != "n-s3cret" {
		t.Fatalf("name = %#v, want the unmarked value", v)
	}
	if s := got["mock_ref.shown"]; strings.Join(s.SensitiveAttrs, ",") != "target" {
		t.Fatalf("SensitiveAttrs = %v, want [target]", s.SensitiveAttrs)
	}
}

func TestSensitiveValueErrors(t *testing.T) {
	for src, want := range map[string]string{
		`resource "mock_thing" "k" {
  for_each = data.baseplate_remote_state.assets.outputs.keys
  name     = each.key
}`: "for_each value is sensitive",
		`output "leak" { value = data.baseplate_remote_state.assets.outputs.token }`: `output "leak" refers to sensitive values`,
	} {
		if _, err := loadWith(t, assetsData+src, stubReader(secretOutputs, nil)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s\nerr = %v, want %q", src, err, want)
		}
	}
	for _, src := range []string{
		`output "ok" {
  value     = data.baseplate_remote_state.assets.outputs.token
  sensitive = true
}`,
		`output "ok" { value = nonsensitive(data.baseplate_remote_state.assets.outputs.token) }`,
		`resource "mock_thing" "k" {
  for_each = nonsensitive(data.baseplate_remote_state.assets.outputs.keys)
  name     = each.key
}`,
	} {
		if _, err := loadWith(t, assetsData+src, stubReader(secretOutputs, nil)); err != nil {
			t.Errorf("%s\nerr = %v", src, err)
		}
	}
}

// An output that uses an attribute state records as sensitive must be
// sensitive too.
func TestSensitiveStateAttributeInOutput(t *testing.T) {
	cfg, err := loadWith(t, `
resource "mock_thing" "a" { name = "x" }
output "name" { value = mock_thing.a.name }
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	inst := map[string]cty.Value{"mock_thing.a": cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("x").Mark(plan.SensitiveMark), "id": cty.StringVal("1")})}
	if _, err := cfg.EvalOutputs(inst); err == nil || !strings.Contains(err.Error(), "refers to sensitive values") {
		t.Fatalf("err = %v", err)
	}
}
