package resources_test

import (
	"testing"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/resourcedocs"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox"
)

// Every resource must implement Documented, and every DecodeSpec attribute must
// have a description — Generate returns an error otherwise.
func TestAllResourcesDocumented(t *testing.T) {
	p := roblox.SchemaProvider()
	for typ, r := range p.Resources() {
		if _, ok := r.(provider.Documented); !ok {
			t.Errorf("%s does not implement provider.Documented", typ)
		}
	}
	if _, err := resourcedocs.Generate(p); err != nil {
		t.Fatalf("Generate over the real resources failed (missing/stale descriptions?): %v", err)
	}
}
