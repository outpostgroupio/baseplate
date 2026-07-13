package resourcedocs

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestAttrTypeName(t *testing.T) {
	cases := []struct {
		ty   cty.Type
		want string
	}{
		{cty.String, "string"},
		{cty.Bool, "bool"},
		{cty.Number, "number"},
	}
	for _, c := range cases {
		if got := attrTypeName(c.ty); got != c.want {
			t.Errorf("attrTypeName(%s) = %q, want %q", c.ty.FriendlyName(), got, c.want)
		}
	}
}

func TestAttrKind(t *testing.T) {
	cases := []struct {
		required, createOnly, sensitive, trigger bool
		want                                     string
	}{
		{true, false, false, false, "required, diffable"},
		{false, false, false, false, "optional, diffable"},
		{true, true, false, false, "required, create-only"},
		{false, true, false, false, "optional, create-only"},
		{true, true, true, false, "required, create-only, sensitive"},
		{true, false, true, false, "required, diffable, sensitive"},
		{true, true, false, true, "required, trigger"},
		{true, true, true, true, "required, trigger, sensitive"},
	}
	for _, c := range cases {
		if got := attrKind(c.required, c.createOnly, c.sensitive, c.trigger); got != c.want {
			t.Errorf("attrKind(%v,%v,%v,%v) = %q, want %q", c.required, c.createOnly, c.sensitive, c.trigger, got, c.want)
		}
	}
}
