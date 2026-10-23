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

// loadSrc writes src to a temp config file and loads it with the mock provider.
func loadSrc(t *testing.T, src string) ([]plan.DesiredResource, []plan.ImportBlock, error) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "c.bp.hcl")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, res, imps, err := Load(p, mock.New())
	return res, imps, err
}

func mustLoad(t *testing.T, src string) ([]plan.DesiredResource, []plan.ImportBlock) {
	t.Helper()
	res, imps, err := loadSrc(t, src)
	if err != nil {
		t.Fatal(err)
	}
	return res, imps
}

func wantErr(t *testing.T, src string, wants ...string) {
	t.Helper()
	_, _, err := loadSrc(t, src)
	if err == nil {
		t.Fatalf("expected an error containing %q", wants)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q does not contain %q", err, w)
		}
	}
}

func byAddr(res []plan.DesiredResource) map[string]plan.DesiredResource {
	out := map[string]plan.DesiredResource{}
	for _, r := range res {
		out[r.Address()] = r
	}
	return out
}

func addrs(res []plan.DesiredResource) []string {
	var out []string
	for _, r := range res {
		out = append(out, r.Address())
	}
	return out
}

func TestForEachOverMap(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "set" {
  for_each = { b = "Bee", a = "Ay" }
  name     = each.value
}
`)
	if got := addrs(res); !reflect.DeepEqual(got, []string{`mock_thing.set["a"]`, `mock_thing.set["b"]`}) {
		t.Fatalf("instances = %v (want lexical key order)", got)
	}
	a := res[0]
	if a.Type != "mock_thing" || a.Name != "set" || a.Key != "a" || a.Attributes["name"].AsString() != "Ay" {
		t.Fatalf("instance a = %+v", a)
	}
	if _, ok := a.Attributes["for_each"]; ok {
		t.Fatal("for_each is a meta-argument and must not become an attribute")
	}
}

func TestForEachOverSetOfObjectsMapValue(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "sfx" {
  for_each = { for f in toset(["boom.ogg", "hit.ogg"]) : f => { name = trimsuffix(f, ".ogg") } }
  name     = each.value.name
}
`)
	m := byAddr(res)
	if m[`mock_thing.sfx["hit.ogg"]`].Attributes["name"].AsString() != "hit" {
		t.Fatalf("each.value object not usable: %+v", res)
	}
}

func TestForEachOverSet(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "set" {
  for_each = toset(["x", "y"])
  name     = "n-${each.key}-${each.value}"
}
`)
	m := byAddr(res)
	if len(m) != 2 || m[`mock_thing.set["y"]`].Attributes["name"].AsString() != "n-y-y" {
		t.Fatalf("set expansion wrong: %+v", res)
	}
}

func TestForEachEmptyGivesNoInstances(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "set" {
  for_each = toset([])
  name     = each.key
}
`)
	if len(res) != 0 {
		t.Fatalf("empty for_each must give zero instances, got %v", addrs(res))
	}
}

func TestForEachRejectsList(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "set" {
  for_each = ["a", "b"]
  name     = each.key
}
`, "mock_thing.set: for_each must be a map or a set of strings; got list", "toset(...)")
}

func TestForEachRejectsSetOfNumbers(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "set" {
  for_each = toset([1, 2])
  name     = "x"
}
`, "set of strings")
}

func TestForEachRejectsApplyTimeValue(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "a" { name = "root" }
resource "mock_thing" "set" {
  for_each = toset([mock_thing.a.id])
  name     = each.key
}
`, "for_each value depends on mock_thing.a.id, which is only known at apply time")
}

func TestEachOutsideForEach(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "a" { name = each.key }
`, `"each" is only available in a block that sets for_each`)
	wantErr(t, `
locals { x = each.key }
`, `"each" is only available in a block that sets for_each`)
	wantErr(t, `
resource "mock_thing" "a" { name = "x" }
import {
  to = mock_thing.a
  id = each.value
}
`, `"each" is only available in a block that sets for_each`)
}

func TestStaticIndexedRef(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "set" {
  for_each = toset(["ui/gem.png", "ui/coin.png"])
  name     = each.key
}
resource "mock_ref" "b" {
  name   = "child"
  target = mock_thing.set["ui/gem.png"].id
}
`)
	b := byAddr(res)["mock_ref.b"]
	ref := b.Refs["target"]
	if ref.Address != `mock_thing.set["ui/gem.png"]` || ref.Attr != "id" {
		t.Fatalf("ref = %+v", ref)
	}
	if !reflect.DeepEqual(b.DependsOn, []string{`mock_thing.set["ui/gem.png"]`}) {
		t.Fatalf("deps = %v (want only the referenced instance)", b.DependsOn)
	}
	if _, ok := b.Attributes["target"]; ok {
		t.Fatal("referenced attr must not be in literal Attributes")
	}
}

func TestDynamicIndexedRef(t *testing.T) {
	res, _ := mustLoad(t, `
locals { keys = toset(["a", "b"]) }
resource "mock_thing" "set" {
  for_each = local.keys
  name     = each.key
}
resource "mock_ref" "refs" {
  for_each = local.keys
  name     = "ref-${each.key}"
  target   = mock_thing.set[each.key].id
}
`)
	m := byAddr(res)
	for _, k := range []string{"a", "b"} {
		r := m[`mock_ref.refs["`+k+`"]`]
		want := `mock_thing.set["` + k + `"]`
		if r.Refs["target"].Address != want || r.Refs["target"].Attr != "id" {
			t.Fatalf("%s ref = %+v", k, r.Refs)
		}
		if !reflect.DeepEqual(r.DependsOn, []string{want}) {
			t.Fatalf("%s deps = %v, want [%s]", k, r.DependsOn, want)
		}
	}
}

func TestMissingKeyRef(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "set" {
  for_each = toset(["a", "b", "c", "d", "e", "f"])
  name     = each.key
}
resource "mock_ref" "x" {
  name   = "x"
  target = mock_thing.set["nope"].id
}
`, `mock_ref.x: attribute "target": no instance "nope" of mock_thing.set (declared: a, b, c, d, e, …)`)
}

func TestBareAttrRefToKeyedResource(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "set" {
  for_each = toset(["a"])
  name     = each.key
}
resource "mock_ref" "x" {
  name   = "x"
  target = mock_thing.set.id
}
`, `mock_thing.set has for_each; reference a specific instance, e.g. mock_thing.set["<key>"].id`)
}

func TestIndexingSingularResourceIsError(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "a" { name = "x" }
resource "mock_ref" "x" {
  name   = "x"
  target = mock_thing.a["k"].id
}
`, "mock_thing.a does not have for_each")
}

func TestDependencyOnlyRefDependsOnAllInstances(t *testing.T) {
	res, _ := mustLoad(t, `
resource "mock_thing" "set" {
  for_each = toset(["b", "a"])
  name     = each.key
}
resource "mock_child" "c" {
  name   = "child"
  parent = mock_thing.set
}
`)
	c := byAddr(res)["mock_child.c"]
	if !reflect.DeepEqual(c.DependsOn, []string{`mock_thing.set["a"]`, `mock_thing.set["b"]`}) {
		t.Fatalf("deps = %v, want every instance", c.DependsOn)
	}
	if _, ok := c.Attributes["parent"]; ok {
		t.Fatal("a dependency-only reference must not be a literal attribute")
	}
}

func TestLocalsOrderingAndFunctions(t *testing.T) {
	res, _ := mustLoad(t, `
locals {
  full = "${local.prefix}-${upper(local.base)}"
}
locals {
  base   = "gem"
  prefix = "sprite"
}
resource "mock_thing" "a" { name = local.full }
`)
	if got := res[0].Attributes["name"].AsString(); got != "sprite-GEM" {
		t.Fatalf("name = %q", got)
	}
	if len(res[0].Refs) != 0 || len(res[0].DependsOn) != 0 {
		t.Fatalf("a local is not a resource reference: refs=%v deps=%v", res[0].Refs, res[0].DependsOn)
	}
}

func TestLocalsCycleDetected(t *testing.T) {
	wantErr(t, `
locals {
  a = local.b
  b = local.c
  c = local.a
}
`, "dependency cycle", "local.a -> local.b -> local.c -> local.a")
}

func TestLocalsDuplicateAcrossBlocks(t *testing.T) {
	wantErr(t, "locals { a = 1 }\nlocals { a = 2 }\n", `duplicate local "a"`)
}

func TestLocalsCannotReferenceResources(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "a" { name = "x" }
locals { id = mock_thing.a.id }
`, "locals can't reference resources")
}

func TestLocalsUndeclared(t *testing.T) {
	wantErr(t, `locals { a = local.missing }`, `undeclared local "missing"`)
}

func TestPathConfigAndFilesetForEach(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, "assets/ui/gem.png", "assets/ui/coin.png", "assets/notes.txt")
	p := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(p, []byte(`
resource "mock_thing" "sprites" {
  for_each = fileset("${path.config}/assets", "**/*.png")
  name     = "${path.config}/assets/${each.key}"
}
`), 0o644)
	_, res, _, err := Load(p, mock.New())
	if err != nil {
		t.Fatal(err)
	}
	if got := addrs(res); !reflect.DeepEqual(got, []string{`mock_thing.sprites["ui/coin.png"]`, `mock_thing.sprites["ui/gem.png"]`}) {
		t.Fatalf("instances = %v", got)
	}
	want := filepath.ToSlash(dir) + "/assets/ui/gem.png"
	if got := res[1].Attributes["name"].AsString(); got != want {
		t.Fatalf("path.config not the config dir: %q, want %q", got, want)
	}
}

func TestDuplicateResourceBlock(t *testing.T) {
	wantErr(t, "resource \"mock_thing\" \"a\" { name = \"x\" }\nresource \"mock_thing\" \"a\" { name = \"y\" }\n",
		"duplicate resource block mock_thing.a")
}

func TestImportForEach(t *testing.T) {
	_, imps := mustLoad(t, `
locals {
  existing = { "ui/gem.png" = 111, "ui/coin.png" = "222", "ui/old.png" = "333" }
  files    = toset(["ui/gem.png", "ui/coin.png", "ui/new.png"])
}
resource "mock_thing" "sprites" {
  for_each = local.files
  name     = each.key
}
import {
  for_each = { for k, id in local.existing : k => id if contains(local.files, k) }
  to       = mock_thing.sprites[each.key]
  id       = each.value
}
`)
	got := map[string]string{}
	for _, i := range imps {
		got[i.Address] = i.ID
	}
	want := map[string]string{`mock_thing.sprites["ui/coin.png"]`: "222", `mock_thing.sprites["ui/gem.png"]`: "111"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imports = %v, want %v", got, want)
	}
}

func TestImportStaticIndexedTo(t *testing.T) {
	_, imps := mustLoad(t, `
resource "mock_thing" "sprites" {
  for_each = toset(["a"])
  name     = each.key
}
import {
  to = mock_thing.sprites["a"]
  id = "mock_thing/1"
}
`)
	if len(imps) != 1 || imps[0].Address != `mock_thing.sprites["a"]` {
		t.Fatalf("imports = %+v", imps)
	}
}

func TestImportToUnindexedKeyedResource(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "sprites" {
  for_each = toset(["a"])
  name     = each.key
}
import {
  to = mock_thing.sprites
  id = "x"
}
`, "mock_thing.sprites has for_each; import into a specific instance")
}

func TestImportDuplicateExpandedTargets(t *testing.T) {
	wantErr(t, `
resource "mock_thing" "sprites" {
  for_each = toset(["a"])
  name     = each.key
}
import {
  for_each = { x = "1", y = "2" }
  to       = mock_thing.sprites["a"]
  id       = each.value
}
`, `duplicate import target mock_thing.sprites["a"]`)
}

func TestLoadConfigReportsEmptyForEach(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(p, []byte(`
resource "mock_thing" "empty" {
  for_each = fileset("${path.config}", "*.nothing")
  name     = each.key
}
resource "mock_thing" "full" {
  for_each = toset(["a"])
  name     = each.key
}
resource "mock_thing" "solo" { name = "x" }
`), 0o644)
	cfg, err := LoadConfig(p, mock.New())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.EmptyForEach, []string{"mock_thing.empty"}) {
		t.Fatalf("EmptyForEach = %v, want [mock_thing.empty]", cfg.EmptyForEach)
	}
	if len(cfg.Resources) != 2 {
		t.Fatalf("resources = %v", addrs(cfg.Resources))
	}
}
