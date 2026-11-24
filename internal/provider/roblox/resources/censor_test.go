package resources

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestFilteredTextCensored(t *testing.T) {
	s := cty.StringVal
	cases := []struct {
		desired, actual cty.Value
		want            bool
	}{
		{s("Buy the damn sword"), s("Buy the #### sword"), true},
		{s("Épée für dich"), s("Épée ### dich"), true}, // compared by character, not byte
		{s("same"), s("same"), false},                  // nothing filtered
		{s("Buy the sword"), s("Buy the #### sword"), false},
		{s("Buy the damn sword"), s("Buy the #### spear"), false},
		{s("a#b"), s("a#c"), false},
		{s("abc"), cty.NullVal(cty.String), false},
		{cty.NumberIntVal(1), s("#"), false},
	}
	for _, c := range cases {
		if got := (filteredText{}).Censored("description", c.desired, c.actual); got != c.want {
			t.Errorf("Censored(%#v, %#v) = %v, want %v", c.desired, c.actual, got, c.want)
		}
	}
}
