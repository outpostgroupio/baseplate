package roblox

import (
	"testing"

	"github.com/hashicorp/hcl/v2/hcldec"
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
