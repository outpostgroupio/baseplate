package configgen

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

func TestZeroValueNonPrimitiveTypes(t *testing.T) {
	cases := []struct {
		ty   cty.Type
		want cty.Value
	}{
		{cty.DynamicPseudoType, cty.EmptyObjectVal},
		{cty.Map(cty.String), cty.MapValEmpty(cty.String)},
		{cty.List(cty.Number), cty.ListValEmpty(cty.Number)},
		{cty.Set(cty.String), cty.SetValEmpty(cty.String)},
		{cty.EmptyObject, cty.EmptyObjectVal},
		{cty.Object(map[string]cty.Type{"a": cty.Bool}), cty.ObjectVal(map[string]cty.Value{"a": cty.False})},
		{cty.EmptyTuple, cty.EmptyTupleVal},
	}
	for _, c := range cases {
		if got := zeroValue(c.ty); !got.RawEquals(c.want) {
			t.Errorf("zeroValue(%s) = %#v, want %#v", c.ty.FriendlyName(), got, c.want)
		}
	}
}

// A map-like "any" attribute is emitted as an HCL object that decodes back to
// the same value (the --generate-config round trip for experience config).
func TestGenerateStructuralAttributeRoundTrips(t *testing.T) {
	spec := hcldec.ObjectSpec{
		"entries": &hcldec.AttrSpec{Name: "entries", Type: cty.DynamicPseudoType, Required: true},
		"extra":   &hcldec.AttrSpec{Name: "extra", Type: cty.DynamicPseudoType, Required: true},
	}
	entries := cty.ObjectVal(map[string]cty.Value{
		"on":   cty.True,
		"n":    cty.NumberIntVal(100),
		"shop": cty.ObjectVal(map[string]cty.Value{"items": cty.TupleVal([]cty.Value{cty.StringVal("a"), cty.NumberFloatVal(1.5)})}),
	})
	b, err := Generate(fakeRes{typ: "roblox_x", spec: spec}, "a", "1", provider.Attributes{"entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, b)
	if !strings.Contains(out, "extra = {}") {
		t.Fatalf("required unreadable 'any' attr should get an empty-object placeholder:\n%s", out)
	}
	f, diags := hclsyntax.ParseConfig([]byte(out), "gen.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("generated HCL does not parse: %v\n%s", diags, out)
	}
	blk := f.Body.(*hclsyntax.Body).Blocks[0]
	val, diags := hcldec.Decode(blk.Body, spec, nil)
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	if !val.GetAttr("entries").RawEquals(entries) {
		t.Fatalf("round trip mismatch:\n got  %#v\n want %#v", val.GetAttr("entries"), entries)
	}
}
