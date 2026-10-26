package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
)

func loadMoved(t *testing.T, src string) ([]plan.MovedBlock, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.bp.hcl")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p, mock.New())
	if err != nil {
		return nil, err
	}
	return cfg.Moved, nil
}

func wantMoved(t *testing.T, src string, want []plan.MovedBlock) {
	t.Helper()
	got, err := loadMoved(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("moved = %v, want %v", got, want)
	}
}

func wantMovedErr(t *testing.T, src string, wants ...string) {
	t.Helper()
	_, err := loadMoved(t, src)
	if err == nil {
		t.Fatalf("expected an error containing %q", wants)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q does not contain %q", err, w)
		}
	}
}

func TestMovedInstanceRename(t *testing.T) {
	wantMoved(t, `
resource "mock_thing" "set" {
  for_each = toset(["ui/new.png"])
  name     = each.key
}
moved {
  from = mock_thing.set["ui/old.png"]
  to   = mock_thing.set["ui/new.png"]
}
`, []plan.MovedBlock{{From: `mock_thing.set["ui/old.png"]`, To: `mock_thing.set["ui/new.png"]`}})
}

func TestMovedSingularToKeyed(t *testing.T) {
	wantMoved(t, `
resource "mock_thing" "a" {
  for_each = toset(["k"])
  name     = each.key
}
moved {
  from = mock_thing.a
  to   = mock_thing.a["k"]
}
`, []plan.MovedBlock{{From: "mock_thing.a", To: `mock_thing.a["k"]`}})
}

func TestMovedKeyedToSingular(t *testing.T) {
	wantMoved(t, `
resource "mock_thing" "a" {
  name = "x"
}
moved {
  from = mock_thing.a["k"]
  to   = mock_thing.a
}
`, []plan.MovedBlock{{From: `mock_thing.a["k"]`, To: "mock_thing.a"}})
}

func TestMovedResourceRename(t *testing.T) {
	wantMoved(t, `
resource "mock_thing" "new" {
  name = "x"
}
moved {
  from = mock_thing.old
  to   = mock_thing.new
}
`, []plan.MovedBlock{{From: "mock_thing.old", To: "mock_thing.new"}})
}

func TestMovedForEachDirectoryMove(t *testing.T) {
	wantMoved(t, `
locals {
  renames = {
    "ui/gem.png"  = "icons/gem.png"
    "ui/coin.png" = "icons/coin.png"
  }
}
resource "mock_thing" "sprites" {
  for_each = toset(["icons/gem.png", "icons/coin.png"])
  name     = each.key
}
moved {
  for_each = local.renames
  from     = mock_thing.sprites[each.key]
  to       = mock_thing.sprites[each.value]
}
`, []plan.MovedBlock{
		{From: `mock_thing.sprites["ui/coin.png"]`, To: `mock_thing.sprites["icons/coin.png"]`},
		{From: `mock_thing.sprites["ui/gem.png"]`, To: `mock_thing.sprites["icons/gem.png"]`},
	})
}

// The target needn't be declared: like Terraform, the move happens and plan
// then shows a delete at the new address.
func TestMovedToUndeclaredTargetLoads(t *testing.T) {
	wantMoved(t, `
moved {
  from = mock_thing.a
  to   = mock_thing.b
}
`, []plan.MovedBlock{{From: "mock_thing.a", To: "mock_thing.b"}})
}

// A chain through a wholly moved resource is not a conflict: the instance
// move feeds the whole-resource move (x["k"] -> a["k"], then a -> c).
func TestMovedChainThroughWholeResourceMoveLoads(t *testing.T) {
	wantMoved(t, `
moved {
  from = mock_thing.x["k"]
  to   = mock_thing.a["k"]
}
moved {
  from = mock_thing.a
  to   = mock_thing.c
}
moved {
  from = mock_thing.c["k"]
  to   = mock_thing.d["k"]
}
`, []plan.MovedBlock{
		{From: `mock_thing.x["k"]`, To: `mock_thing.a["k"]`},
		{From: "mock_thing.a", To: "mock_thing.c"},
		{From: `mock_thing.c["k"]`, To: `mock_thing.d["k"]`},
	})
}

func TestMovedErrors(t *testing.T) {
	cases := map[string]struct {
		src   string
		wants []string
	}{
		"type change": {`
moved {
  from = mock_thing.a
  to   = mock_child.a
}`, []string{"type differs", "mock_thing vs mock_child"}},
		"unknown type": {`
moved {
  from = nope_thing.a
  to   = nope_thing.b
}`, []string{`unknown resource type "nope_thing"`}},
		"same address": {`
moved {
  from = mock_thing.a
  to   = mock_thing.a
}`, []string{"same address"}},
		"missing to": {`
moved {
  from = mock_thing.a
}`, []string{"requires a `to`"}},
		"missing from": {`
moved {
  to = mock_thing.a
}`, []string{"requires a `from`"}},
		"unsupported argument": {`
moved {
  from = mock_thing.a
  to   = mock_thing.b
  id   = "x"
}`, []string{`unsupported argument "id"`}},
		"not an address": {`
moved {
  from = "mock_thing.a"
  to   = mock_thing.b
}`, []string{"moved `from` must be a resource address"}},
		"attribute traversal": {`
moved {
  from = mock_thing.a.id
  to   = mock_thing.b
}`, []string{"moved `from` must be a resource address"}},
		"each without for_each": {`
moved {
  from = mock_thing.a[each.key]
  to   = mock_thing.b
}`, []string{`"each" is only available`}},
		"for_each list": {`
moved {
  for_each = ["a"]
  from     = mock_thing.a[each.key]
  to       = mock_thing.b[each.key]
}`, []string{"toset"}},
		"from still declared (resource)": {`
resource "mock_thing" "old" {
  name = "x"
}
moved {
  from = mock_thing.old
  to   = mock_thing.new
}`, []string{"mock_thing.old is still declared"}},
		"from still declared (instance)": {`
resource "mock_thing" "set" {
  for_each = toset(["a", "b"])
  name     = each.key
}
moved {
  from = mock_thing.set["a"]
  to   = mock_thing.set["b"]
}`, []string{`mock_thing.set["a"] is still declared`}},
		"from still declared (singular instance)": {`
resource "mock_thing" "a" {
  name = "x"
}
moved {
  from = mock_thing.a
  to   = mock_thing.a["k"]
}`, []string{"mock_thing.a is still declared"}},
		"duplicate from": {`
moved {
  from = mock_thing.a
  to   = mock_thing.b
}
moved {
  from = mock_thing.a
  to   = mock_thing.c
}`, []string{"ambiguous moved blocks", "mock_thing.a is moved to mock_thing.b by the block at", "c.bp.hcl:2", "c.bp.hcl:6"}},
		"duplicate to": {`
moved {
  from = mock_thing.a
  to   = mock_thing.c
}
moved {
  from = mock_thing.b
  to   = mock_thing.c
}`, []string{"ambiguous moved blocks", "mock_thing.c is the destination of both", "c.bp.hcl:2", "c.bp.hcl:6"}},
		"whole-resource move and instance move out of it": {`
moved {
  from = mock_thing.a
  to   = mock_thing.c
}
moved {
  from = mock_thing.a["k"]
  to   = mock_thing.d["k"]
}`, []string{"conflicting moved blocks", "c.bp.hcl:2 moves all of mock_thing.a to mock_thing.c",
			`c.bp.hcl:6 also moves mock_thing.a["k"] out of mock_thing.a`}},
		"instance move out of a wholly moved resource, declared first": {`
moved {
  for_each = toset(["k"])
  from     = mock_thing.a[each.key]
  to       = mock_thing.d[each.key]
}
moved {
  from = mock_thing.a
  to   = mock_thing.c
}`, []string{"conflicting moved blocks", "c.bp.hcl:7 moves all of mock_thing.a", `c.bp.hcl:2 also moves mock_thing.a["k"]`}},
		"whole-resource move and instance move into its destination": {`
moved {
  from = mock_thing.a
  to   = mock_thing.c
}
moved {
  from = mock_thing.x["k"]
  to   = mock_thing.c["k"]
}`, []string{"conflicting moved blocks", `also moves mock_thing.x["k"] into mock_thing.c["k"]`}},
		"for_each value not a string key": {`
moved {
  for_each = { a = 1 }
  from     = mock_thing.s[each.key]
  to       = mock_thing.s[each.value]
}`, []string{"instance key must be a known string"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { wantMovedErr(t, c.src, c.wants...) })
	}
}
