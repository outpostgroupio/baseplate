package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
)

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
