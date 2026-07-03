package config

import (
	"path/filepath"
	"testing"

	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider/mock"
)

func TestLoadResourcesAndDeps(t *testing.T) {
	t.Setenv("BP_TEST_KEY", "secret123")
	pc, resources, err := Load(filepath.Join("..", "..", "testdata", "sample.bp.hcl"), mock.New())
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
	_, resources, err := Load(filepath.Join("..", "..", "testdata", "refs.bp.hcl"), mock.New())
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
