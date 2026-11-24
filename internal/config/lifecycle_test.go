package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
)

func TestLifecyclePreventDestroy(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "prod" {
  name = "prod"
  lifecycle {
    prevent_destroy = true
  }
}
resource "mock_thing" "off" {
  name = "off"
  lifecycle {
    prevent_destroy = false
  }
}
resource "mock_thing" "plain" {
  name = "plain"
}
`)
	got := byAddr(res)
	if !got["mock_thing.prod"].PreventDestroy {
		t.Fatal("mock_thing.prod should carry PreventDestroy")
	}
	if got["mock_thing.off"].PreventDestroy || got["mock_thing.plain"].PreventDestroy {
		t.Fatal("only mock_thing.prod sets prevent_destroy = true")
	}
	// The lifecycle block is a meta-argument: it never reaches the resource's attributes.
	for _, r := range res {
		if _, ok := r.Attributes["lifecycle"]; ok {
			t.Fatalf("%s: lifecycle leaked into attributes", r.Address())
		}
		if len(r.Attributes) != 1 {
			t.Fatalf("%s: expected only `name`, got %v", r.Address(), r.Attributes)
		}
	}
}

func TestLifecyclePreventDestroyWithForEach(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "env" {
  for_each = toset(["prod", "staging"])
  name     = each.key
  lifecycle {
    prevent_destroy = true
  }
}
`)
	if len(res) != 2 {
		t.Fatalf("expected 2 instances, got %v", addrs(res))
	}
	for _, r := range res {
		if !r.PreventDestroy {
			t.Fatalf("%s: prevent_destroy applies to every instance", r.Address())
		}
	}
}

func TestLifecycleConfigListsProtectedResources(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.bp.hcl")
	src := `
resource "mock_thing" "b" {
  name = "b"
  lifecycle { prevent_destroy = true }
}
resource "mock_thing" "a" {
  for_each = {}
  name     = "a"
  lifecycle { prevent_destroy = true }
}
resource "mock_thing" "c" {
  name = "c"
}
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p, mock.New())
	if err != nil {
		t.Fatal(err)
	}
	// An empty for_each block still protects whatever instances state holds.
	if want := []string{"mock_thing.a", "mock_thing.b"}; !reflect.DeepEqual(cfg.PreventDestroy, want) {
		t.Fatalf("PreventDestroy = %v, want %v", cfg.PreventDestroy, want)
	}
}

func TestLifecycleRejectsNonBool(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "a" {
  name = "a"
  lifecycle { prevent_destroy = "yes" }
}
`, "mock_thing.a", "prevent_destroy", "bool")
	wantErr(t, `
resource "mock_thing" "a" {
  name = "a"
  lifecycle { prevent_destroy = 1 }
}
`, "prevent_destroy", "bool")
	wantErr(t, `
resource "mock_thing" "a" {
  name = "a"
  lifecycle { prevent_destroy = null }
}
`, "prevent_destroy", "bool")
}

func TestLifecycleRejectsNonLiteral(t *testing.T) {
	wantErr(t, `
locals { protect = true }
resource "mock_thing" "a" {
  name = "a"
  lifecycle { prevent_destroy = local.protect }
}
`, "prevent_destroy", "literal")
	wantErr(t, `
resource "mock_thing" "a" {
  for_each = toset(["x"])
  name     = each.key
  lifecycle { prevent_destroy = each.key == "x" }
}
`, "prevent_destroy", "literal")
}

func TestLifecycleRejectsUnsupportedArguments(t *testing.T) {
	for _, arg := range []string{
		`ignore_changes = [name]`,
		`create_before_destroy = true`,
		`bogus = 1`,
	} {
		wantErr(t, `
resource "mock_thing" "a" {
  name = "a"
  lifecycle {
    `+arg+`
  }
}
`, "mock_thing.a", "not supported", "prevent_destroy",
			"replace_triggered_by", "ignore_changes", "create_before_destroy")
	}
	// Nested blocks (Terraform's precondition/postcondition) are rejected too.
	wantErr(t, `
resource "mock_thing" "a" {
  name = "a"
  lifecycle {
    precondition {
      condition     = true
      error_message = "x"
    }
  }
}
`, "mock_thing.a", "precondition", "not supported")
}

func TestLifecycleRejectsDuplicateBlock(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "a" {
  name = "a"
  lifecycle { prevent_destroy = true }
  lifecycle { prevent_destroy = false }
}
`, "mock_thing.a", "duplicate lifecycle block")
}

func TestLifecycleRejectsLabels(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "a" {
  name = "a"
  lifecycle "x" { prevent_destroy = true }
}
`)
}

func TestLifecycleReplaceTriggeredBy(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "hash" {
  for_each = toset(["x", "y"])
  name     = each.key
}
resource "mock_thing" "single" {
  name = "s"
}
resource "mock_thing" "map" {
  for_each = toset(["x", "y"])
  name     = each.key
  lifecycle {
    replace_triggered_by = [mock_thing.hash[each.key], mock_thing.single.name]
  }
}
`)
	got := byAddr(res)
	m := got[`mock_thing.map["x"]`]
	want := []provider.Ref{{Address: `mock_thing.hash["x"]`}, {Address: "mock_thing.single", Attr: "name"}}
	if !reflect.DeepEqual(m.ReplaceTriggeredBy, want) {
		t.Fatalf("ReplaceTriggeredBy = %+v, want %+v", m.ReplaceTriggeredBy, want)
	}
	// The triggers are dependencies, so they're planned first.
	if !reflect.DeepEqual(m.DependsOn, []string{`mock_thing.hash["x"]`, "mock_thing.single"}) {
		t.Fatalf("DependsOn = %v", m.DependsOn)
	}
}

func TestLifecycleReplaceTriggeredByRejects(t *testing.T) {
	for arg, want := range map[string]string{
		`replace_triggered_by = mock_thing.b`:       "must be a list",
		`replace_triggered_by = ["mock_thing.b"]`:   "must be a resource reference",
		`replace_triggered_by = [local.x]`:          "must be a resource reference",
		`replace_triggered_by = [mock_thing.a]`:     "the resource itself",
		`replace_triggered_by = [mock_thing.nope]`:  "must be a resource reference",
		`replace_triggered_by = [mock_thing.b.x.y]`: "whole attribute",
	} {
		wantErr(t, `
locals { x = 1 }
resource "mock_thing" "b" {
  name = "b"
}
resource "mock_thing" "a" {
  name = "a"
  lifecycle {
    `+arg+`
  }
}
`, "replace_triggered_by", want)
	}
}
