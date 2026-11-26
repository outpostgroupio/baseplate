package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox"
)

func loadRoblox(t *testing.T, src string) ([]plan.DesiredResource, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.bp.hcl")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, res, _, err := Load(p, roblox.SchemaProvider())
	return res, err
}

const nestedAssets = `
resource "roblox_asset" "store" {
  for_each        = toset(["a.png", "b.png"])
  file            = each.key
  display_name    = each.key
  description     = each.key
  creator_user_id = 1
}
`

// A reference inside a list of objects stays in Attributes as an unknown at
// its path, is recorded in PathRefs, and makes the resource depend on its
// target. Literal elements and fields are kept as written.
func TestLoadReferenceInsideListOfObjects(t *testing.T) {
	res, err := loadRoblox(t, nestedAssets+`
resource "roblox_place_media" "lobby" {
  place_id = "1"
  thumbnails = [
    { asset_id = "106681856624842", alt_text = "Three empires" },
    { asset_id = roblox_asset.store["b.png"].id, alt_text = "A storm" },
  ]
}
`)
	if err != nil {
		t.Fatal(err)
	}
	d := byAddr(res)["roblox_place_media.lobby"]
	th, ok := d.Attributes["thumbnails"]
	if !ok {
		t.Fatalf("thumbnails dropped from Attributes: %+v", d.Attributes)
	}
	if th.IsWhollyKnown() {
		t.Fatalf("thumbnails = %#v, want an unknown asset_id in the second element", th)
	}
	first := th.Index(cty.NumberIntVal(0))
	if first.GetAttr("asset_id").AsString() != "106681856624842" {
		t.Fatalf("literal element changed: %#v", first)
	}
	second := th.Index(cty.NumberIntVal(1))
	if second.GetAttr("asset_id").IsKnown() || second.GetAttr("alt_text").AsString() != "A storm" {
		t.Fatalf("second element = %#v", second)
	}
	prs := d.PathRefs["thumbnails"]
	if len(prs) != 1 {
		t.Fatalf("PathRefs = %+v, want one", d.PathRefs)
	}
	want := cty.Path{cty.IndexStep{Key: cty.NumberIntVal(1)}, cty.GetAttrStep{Name: "asset_id"}}
	if !prs[0].Path.Equals(want) || prs[0].Ref != (provider.Ref{Address: `roblox_asset.store["b.png"]`, Attr: "id"}) {
		t.Fatalf("PathRef = %#v", prs[0])
	}
	if len(d.Refs) != 0 {
		t.Fatalf("Refs = %+v, want none (the attribute isn't a whole reference)", d.Refs)
	}
	if strings.Join(d.DependsOn, ",") != `roblox_asset.store["b.png"]` {
		t.Fatalf("DependsOn = %v", d.DependsOn)
	}
}

// Inside a for_each resource the instance key can come from each.key, as at the
// top level.
func TestLoadReferenceInsideListUsesEachKey(t *testing.T) {
	res, err := loadRoblox(t, nestedAssets+`
resource "roblox_place_media" "m" {
  for_each   = toset(["a.png", "b.png"])
  place_id   = "1"
  thumbnails = [{ asset_id = roblox_asset.store[each.key].id }]
}
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a.png", "b.png"} {
		d := byAddr(res)[`roblox_place_media.m["`+k+`"]`]
		prs := d.PathRefs["thumbnails"]
		if len(prs) != 1 || prs[0].Address != `roblox_asset.store["`+k+`"]` {
			t.Fatalf("%s: PathRefs = %+v", k, d.PathRefs)
		}
	}
}

// A reference as a value in a map attribute decodes to an object; it is found
// by its key.
func TestLoadReferenceInsideMap(t *testing.T) {
	res, err := loadRoblox(t, `
resource "roblox_developer_product" "gems" {
  universe_id = "1"
  name        = "Gems"
  price       = 10
}
resource "roblox_experience_config" "flags" {
  universe_id = "1"
  entries = {
    on      = true
    product = roblox_developer_product.gems.id
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	d := byAddr(res)["roblox_experience_config.flags"]
	prs := d.PathRefs["entries"]
	if len(prs) != 1 || prs[0].Address != "roblox_developer_product.gems" || prs[0].Attr != "id" {
		t.Fatalf("PathRefs = %+v", d.PathRefs)
	}
	if !d.Attributes["entries"].GetAttr("on").True() {
		t.Fatalf("entries = %#v", d.Attributes["entries"])
	}
}

// A reference inside a list but inside some other expression (a template, a
// function call, a for expression) is still unsupported.
func TestLoadReferenceInsideListUnsupportedForms(t *testing.T) {
	for _, thumbs := range []string{
		`[{ asset_id = "${roblox_asset.store["a.png"].id}" }]`,
		`[{ asset_id = lower(roblox_asset.store["a.png"].id) }]`,
		`[for k, a in roblox_asset.store : { asset_id = a.id }]`,
		`[{ asset_id = roblox_asset.store["a.png"].id[0] }]`,
	} {
		_, err := loadRoblox(t, nestedAssets+`
resource "roblox_place_media" "m" {
  place_id   = "1"
  thumbnails = `+thumbs+`
}
`)
		if err == nil || !strings.Contains(err.Error(), "unsupported way") {
			t.Fatalf("%s: err = %v, want an unsupported-reference error", thumbs, err)
		}
	}
}

// A missing instance key inside a list is reported like one at the top level.
func TestLoadReferenceInsideListMissingKey(t *testing.T) {
	_, err := loadRoblox(t, nestedAssets+`
resource "roblox_place_media" "m" {
  place_id   = "1"
  thumbnails = [{ asset_id = roblox_asset.store["c.png"].id }]
}
`)
	if err == nil || !strings.Contains(err.Error(), `no instance "c.png"`) {
		t.Fatalf("err = %v", err)
	}
}
