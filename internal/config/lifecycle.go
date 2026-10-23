package config

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

// unsupportedLifecycle is appended to every rejected lifecycle argument, so the
// error says what Baseplate does support and which Terraform arguments are
// known but not implemented yet.
const unsupportedLifecycle = "Baseplate supports only prevent_destroy (ignore_changes, create_before_destroy and replace_triggered_by are not yet supported)"

// lifecycle is what a resource block's `lifecycle` meta-block declares.
type lifecycle struct {
	preventDestroy bool
}

// decodeLifecycle reads the (at most one) `lifecycle` block of a resource. Like
// Terraform, its values must be literals: they are decoded without an eval
// context, so references, locals, each.* and functions are all rejected.
func (l *loader) decodeLifecycle(blocks hcl.Blocks, base string) (lifecycle, error) {
	var lc lifecycle
	if len(blocks) == 0 {
		return lc, nil
	}
	if len(blocks) > 1 {
		return lc, fmt.Errorf("%s: %s: duplicate lifecycle block (first at %s); a resource may have only one", l.path, base, blocks[0].DefRange)
	}
	b := blocks[0]
	syn, ok := b.Body.(*hclsyntax.Body)
	if !ok {
		return lc, fmt.Errorf("%s: %s: lifecycle block must be native HCL syntax", l.path, base)
	}
	if len(syn.Blocks) > 0 {
		nb := syn.Blocks[0]
		return lc, fmt.Errorf("%s: %s: lifecycle block %q is not supported; %s", nb.TypeRange, base, nb.Type, unsupportedLifecycle)
	}
	names := make([]string, 0, len(syn.Attributes))
	for name := range syn.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a := syn.Attributes[name]
		if name != "prevent_destroy" {
			return lc, fmt.Errorf("%s: %s: lifecycle argument %q is not supported; %s", a.NameRange, base, name, unsupportedLifecycle)
		}
		if len(a.Expr.Variables()) > 0 {
			return lc, fmt.Errorf("%s: %s: lifecycle.prevent_destroy must be a literal true or false, not an expression that references other values", a.NameRange, base)
		}
		v, d := a.Expr.Value(nil)
		if d.HasErrors() {
			return lc, fmt.Errorf("%s: %s: lifecycle.prevent_destroy must be a literal true or false: %s", a.NameRange, base, d.Error())
		}
		bv, err := convert.Convert(v, cty.Bool)
		if err != nil || bv.IsNull() || !bv.IsKnown() {
			return lc, fmt.Errorf("%s: %s: lifecycle.prevent_destroy must be a bool (true or false); got %s", a.NameRange, base, describeValue(v))
		}
		lc.preventDestroy = bv.True()
	}
	return lc, nil
}

func describeValue(v cty.Value) string {
	if v.IsNull() {
		return "null"
	}
	return v.Type().FriendlyName()
}
