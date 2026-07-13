package resourcedocs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/outpostgroupio/baseplate/internal/provider/resourcedocs"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox"
)

// TestResourceReferenceUpToDate fails if docs/resource-reference.md differs from
// freshly generated output. Fix: run `go generate ./...` and commit.
func TestResourceReferenceUpToDate(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	// internal/provider/resourcedocs/golden_test.go → repo root is three levels up.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	path := filepath.Join(root, "docs", "resource-reference.md")

	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resourcedocs.Generate(roblox.SchemaProvider())
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != got {
		t.Fatalf("%s is stale — run `go generate ./...` and commit", path)
	}
}
