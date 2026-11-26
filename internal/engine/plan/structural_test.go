package plan

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// Structural (map/object/list) attributes: the config and the read-back can
// carry the same value under different cty types; that must not be drift.
func TestDiffAttrsStructuralTypeDifferencesAreNotDrift(t *testing.T) {
	cases := map[string][2]cty.Value{
		"map vs object": {
			cty.MapVal(map[string]cty.Value{"a": cty.StringVal("x")}),
			cty.ObjectVal(map[string]cty.Value{"a": cty.StringVal("x")}),
		},
		"list vs tuple": {
			cty.ListVal([]cty.Value{cty.NumberIntVal(1)}),
			cty.TupleVal([]cty.Value{cty.NumberIntVal(1)}),
		},
		"nested number formatting": {
			cty.ObjectVal(map[string]cty.Value{"n": cty.MustParseNumberVal("100.0")}),
			cty.ObjectVal(map[string]cty.Value{"n": cty.NumberIntVal(100)}),
		},
	}
	for name, c := range cases {
		d := diffAttrs(provider.Attributes{"entries": c[0]}, provider.Attributes{"entries": c[1]}, nil)
		if len(d) != 0 {
			t.Errorf("%s: unexpected diff %+v", name, d)
		}
	}
}

func TestDiffAttrsStructuralRealChangesStillDiff(t *testing.T) {
	cases := map[string][2]cty.Value{
		"value changed": {
			cty.ObjectVal(map[string]cty.Value{"a": cty.True}),
			cty.ObjectVal(map[string]cty.Value{"a": cty.False}),
		},
		"string vs number": {
			cty.ObjectVal(map[string]cty.Value{"a": cty.StringVal("1")}),
			cty.ObjectVal(map[string]cty.Value{"a": cty.NumberIntVal(1)}),
		},
		"key removed": {
			cty.ObjectVal(map[string]cty.Value{"a": cty.True, "b": cty.True}),
			cty.ObjectVal(map[string]cty.Value{"a": cty.True}),
		},
		"null vs empty": {
			cty.NullVal(cty.DynamicPseudoType),
			cty.EmptyObjectVal,
		},
	}
	for name, c := range cases {
		d := diffAttrs(provider.Attributes{"entries": c[0]}, provider.Attributes{"entries": c[1]}, nil)
		if len(d) != 1 {
			t.Errorf("%s: want 1 diff, got %+v", name, d)
		}
	}
}

func TestRenderStructuralValuesAsJSON(t *testing.T) {
	entries := cty.ObjectVal(map[string]cty.Value{
		"on":   cty.True,
		"shop": cty.ObjectVal(map[string]cty.Value{"items": cty.TupleVal([]cty.Value{cty.StringVal("a")})}),
	})
	pl := &Plan{Changes: []Change{{
		Address: "roblox_experience_config.flags", Type: "roblox_experience_config", ID: "1/InExperienceConfig",
		Action:    provider.ActionUpdate,
		Desired:   provider.Attributes{"entries": entries},
		AttrDiffs: []AttrDiff{{Name: "entries", Old: cty.EmptyObjectVal, New: entries}},
	}}}
	want := `{"on":true,"shop":{"items":["a"]}}`
	if got := pl.RenderText(false); !strings.Contains(got, "entries: {} => "+want) {
		t.Fatalf("text render missing JSON value:\n%s", got)
	}
	jc := pl.JSONChanges()
	if jc[0].Attrs[0].Old != "{}" || jc[0].Attrs[0].New != want {
		t.Fatalf("json changes = %+v", jc[0].Attrs)
	}
	// A reference inside an object: only that field is unknown.
	partly := cty.ObjectVal(map[string]cty.Value{"a": cty.UnknownVal(cty.String), "b": cty.StringVal("x"), "c": cty.ListVal([]cty.Value{cty.StringVal("y")})})
	if got := ctyString(partly); got != `{"a":(known after apply),"b":"x","c":["y"]}` {
		t.Fatalf("partially unknown = %q", got)
	}
}
