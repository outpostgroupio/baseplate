package roblox

import (
	"sort"
	"testing"

	"github.com/hashicorp/hcl/v2/hcldec"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

func TestNewRequiresAPIKey(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("expected an error when the API key is empty")
	}
}

func TestNewAssemblesResources(t *testing.T) {
	p, err := New("some-key")
	if err != nil {
		t.Fatal(err)
	}
	res := p.Resources()
	for _, typ := range []string{"roblox_experience", "roblox_asset", "roblox_developer_product"} {
		if _, ok := res[typ]; !ok {
			t.Fatalf("missing resource type %q", typ)
		}
	}
}

// TestNewAndSchemaProviderResourceSetsMatch guards against New() and
// SchemaProvider() drifting apart (e.g. a resource added to one but not the
// other), since the doc generator (and other introspection) uses
// SchemaProvider() as a stand-in for the real, network-backed provider built
// by New(). New("x") succeeds without any network call — it only constructs
// clients — so it's safe to call here.
func TestNewAndSchemaProviderResourceSetsMatch(t *testing.T) {
	real, err := New("x")
	if err != nil {
		t.Fatal(err)
	}
	schema := SchemaProvider()

	realKeys := resourceKeys(real.Resources())
	schemaKeys := resourceKeys(schema.Resources())

	if len(realKeys) != len(schemaKeys) {
		t.Fatalf("resource type sets differ:\n New():           %v\n SchemaProvider(): %v", realKeys, schemaKeys)
	}
	for i := range realKeys {
		if realKeys[i] != schemaKeys[i] {
			t.Fatalf("resource type sets differ:\n New():           %v\n SchemaProvider(): %v", realKeys, schemaKeys)
		}
	}
}

func resourceKeys(m map[string]provider.Resource) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestSchemaProviderHasAllResourcesWithSchemas(t *testing.T) {
	p := SchemaProvider()
	want := []string{
		"roblox_experience", "roblox_asset", "roblox_developer_product",
		"roblox_place", "roblox_universe_secret", "roblox_game_pass",
	}
	for _, typ := range want {
		r, ok := p.Resources()[typ]
		if !ok {
			t.Fatalf("SchemaProvider missing %s", typ)
		}
		// DecodeSpec/Computed must work with nil clients (no network).
		if _, ok := r.DecodeSpec().(hcldec.ObjectSpec); !ok {
			t.Errorf("%s DecodeSpec is not an ObjectSpec", typ)
		}
		_ = r.Computed()
	}
}
