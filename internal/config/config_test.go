package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox"
)

// A map-valued attribute (roblox_experience_config.entries) decodes as native
// HCL: mixed bool/number/string/object values keep their types.
func TestLoadMapAttributeKeepsValueTypes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.bp.hcl")
	os.WriteFile(p, []byte(`
resource "roblox_experience_config" "flags" {
  universe_id = "1"
  entries = {
    on   = true
    n    = 100
    s    = "x"
    nest = { a = [1, "b"] }
  }
}
`), 0o644)
	_, res, _, err := Load(p, roblox.SchemaProvider())
	if err != nil {
		t.Fatal(err)
	}
	e := res[0].Attributes["entries"]
	if !e.Type().IsObjectType() {
		t.Fatalf("entries type = %s", e.Type().FriendlyName())
	}
	if e.GetAttr("on").Type() != cty.Bool || e.GetAttr("n").Type() != cty.Number || e.GetAttr("s").Type() != cty.String {
		t.Fatalf("value types not preserved: %#v", e)
	}
}

// A reference nested inside a map attribute leaves the container known but an
// element unknown; Load must reject it rather than pass an unknown to apply.
func TestLoadRejectsReferenceNestedInMap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.bp.hcl")
	os.WriteFile(p, []byte(`
resource "roblox_developer_product" "gems" {
  universe_id = "1"
  name        = "Gems"
  price       = 10
}
resource "roblox_experience_config" "flags" {
  universe_id = "1"
  entries = {
    product = roblox_developer_product.gems.id
  }
}
`), 0o644)
	_, _, _, err := Load(p, roblox.SchemaProvider())
	if err == nil || !strings.Contains(err.Error(), "unsupported way") {
		t.Fatalf("err = %v, want an unsupported-reference error", err)
	}
}

func TestLoadBackendParsesHTTPBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(p, []byte(`
backend "http" {
  address  = "https://example.com/state"
  username = "u"
  password = env("BP_TEST_PW")
}
resource "mock_thing" "a" { name = "x" }
`), 0o644)
	t.Setenv("BP_TEST_PW", "secret")

	bc, err := LoadBackend(p)
	if err != nil {
		t.Fatal(err)
	}
	if bc == nil || bc.Type != "http" {
		t.Fatalf("want http backend, got %+v", bc)
	}
	if bc.Attributes["address"].AsString() != "https://example.com/state" {
		t.Fatalf("address wrong: %v", bc.Attributes["address"])
	}
	if bc.Attributes["password"].AsString() != "secret" {
		t.Fatalf("env() not resolved in backend block: %v", bc.Attributes["password"])
	}
}

func TestLoadBackendAbsentAndMissingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(p, []byte(`resource "mock_thing" "a" { name = "x" }`), 0o644)
	if bc, err := LoadBackend(p); err != nil || bc != nil {
		t.Fatalf("no block -> nil,nil; got %+v, %v", bc, err)
	}
	if bc, err := LoadBackend(filepath.Join(dir, "nope.hcl")); err != nil || bc != nil {
		t.Fatalf("missing file -> nil,nil; got %+v, %v", bc, err)
	}
}

func TestLoadBackendRejectsTwoBlocks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(p, []byte("backend \"http\" { address = \"a\" }\nbackend \"http\" { address = \"b\" }\n"), 0o644)
	if _, err := LoadBackend(p); err == nil {
		t.Fatal("expected error for two backend blocks")
	}
}

// C1 regression: a config WITH a backend block must still load via config.Load.
func TestLoadToleratesBackendBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(p, []byte("backend \"http\" { address = \"a\" }\nresource \"mock_thing\" \"a\" { name = \"x\" }\n"), 0o644)
	_, res, _, err := Load(p, mock.New())
	if err != nil {
		t.Fatalf("Load must tolerate a backend block: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 resource, got %d", len(res))
	}
}

func TestLoadResourcesAndDeps(t *testing.T) {
	t.Setenv("BP_TEST_KEY", "secret123")
	pc, resources, _, err := Load(filepath.Join("..", "..", "testdata", "sample.bp.hcl"), mock.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	if pc.Attributes["api_key"].AsString() != "secret123" {
		t.Fatalf("env() not resolved, got %v", pc.Attributes["api_key"])
	}
	found := false
	for _, r := range resources {
		if r.Address() == "mock_child.c" {
			found = true
			if len(r.DependsOn) != 1 || r.DependsOn[0] != "mock_thing.a" {
				t.Fatalf("expected dep on mock_thing.a, got %v", r.DependsOn)
			}
		}
	}
	if !found {
		t.Fatal("mock_child.c not found")
	}
}

func TestLoadCapturesReferences(t *testing.T) {
	_, resources, _, err := Load(filepath.Join("..", "..", "testdata", "refs.bp.hcl"), mock.New())
	if err != nil {
		t.Fatal(err)
	}
	var b *plan.DesiredResource
	for i := range resources {
		if resources[i].Address() == "mock_ref.b" {
			b = &resources[i]
		}
	}
	if b == nil {
		t.Fatal("mock_ref.b not found")
	}
	ref, ok := b.Refs["target"]
	if !ok || ref.Address != "mock_thing.a" || ref.Attr != "id" {
		t.Fatalf("expected target -> mock_thing.a.id, got %+v (refs=%+v)", ref, b.Refs)
	}
	if _, present := b.Attributes["target"]; present {
		t.Fatal("referenced attr must not be pre-filled in Attributes")
	}
	if len(b.DependsOn) != 1 || b.DependsOn[0] != "mock_thing.a" {
		t.Fatalf("expected dependency on mock_thing.a, got %v", b.DependsOn)
	}
}

func TestLoadOmitsUnsetOptionalAttrs(t *testing.T) {
	// mock_child has an optional "parent" attribute; when the user doesn't write
	// it, hcldec decodes it as null. Load must omit it from Attributes so it is
	// not treated as a declared attribute (which would diff as null forever).
	_, resources, _, err := Load(filepath.Join("..", "..", "testdata", "optional.bp.hcl"), mock.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	attrs := resources[0].Attributes
	if _, ok := attrs["name"]; !ok {
		t.Fatal("declared attribute 'name' should be present")
	}
	if _, ok := attrs["parent"]; ok {
		t.Fatalf("unset optional attribute 'parent' must be omitted, got attrs=%+v", attrs)
	}
}

func TestLoadRejectsWrappedReference(t *testing.T) {
	_, _, _, err := Load(filepath.Join("..", "..", "testdata", "badref.bp.hcl"), mock.New())
	if err == nil {
		t.Fatal("expected an error for a reference wrapped in a string template")
	}
	if !strings.Contains(err.Error(), "unsupported way") {
		t.Fatalf("error should explain the unsupported reference form, got: %v", err)
	}
}

func TestLoadParsesImportBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(path, []byte(`
resource "mock_thing" "a" {
  name = "root"
}

import {
  to = mock_thing.a
  id = "mock_thing/7"
}
`), 0o644)

	_, _, imports, err := Load(path, mock.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 1 {
		t.Fatalf("expected 1 import block, got %d", len(imports))
	}
	if imports[0].Address != "mock_thing.a" || imports[0].ID != "mock_thing/7" {
		t.Fatalf("import = %+v", imports[0])
	}
}

func TestLoadRejectsImportWithoutTo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.bp.hcl")
	os.WriteFile(path, []byte("import {\n  id = \"x\"\n}\n"), 0o644)
	if _, _, _, err := Load(path, mock.New()); err == nil {
		t.Fatal("expected an error for an import block missing `to`")
	}
}
