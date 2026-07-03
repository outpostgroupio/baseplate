package roblox

import "testing"

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
