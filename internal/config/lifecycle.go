package config

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// unsupportedLifecycle is appended to every rejected lifecycle argument, so the
// error says what Baseplate does support and which Terraform arguments are
// known but not implemented yet.
const unsupportedLifecycle = "Baseplate supports prevent_destroy and replace_triggered_by (ignore_changes and create_before_destroy are not supported; replaces already create before destroying)"

// lifecycle is what a resource block's `lifecycle` meta-block declares.
type lifecycle struct {
	preventDestroy bool
	// replaceTriggeredBy is the replace_triggered_by list, resolved per
	// instance (it may use each.key) by triggerRefs.
	replaceTriggeredBy *hclsyntax.TupleConsExpr
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
		if name == "replace_triggered_by" {
			list, ok := a.Expr.(*hclsyntax.TupleConsExpr)
			if !ok {
				return lc, fmt.Errorf("%s: %s: lifecycle.replace_triggered_by must be a list of resource references, e.g. [baseplate_data.hash]", a.NameRange, base)
			}
			lc.replaceTriggeredBy = list
			continue
		}
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

// triggerRefs resolves replace_triggered_by for one instance (ctx carries its
// each.key and each.value). Each element must be a reference to a declared
// resource, an instance of one, or one's attribute; a bare reference to a
// for_each resource stands for all of its instances.
func (l *loader) triggerRefs(list *hclsyntax.TupleConsExpr, ctx *hcl.EvalContext, inst string) ([]provider.Ref, error) {
	if list == nil {
		return nil, nil
	}
	var refs []provider.Ref
	for _, e := range list.Exprs {
		t, ok, err := l.resolveRef(e, ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: lifecycle.replace_triggered_by: %w", l.path, inst, err)
		}
		if !ok || (t.partial && t.attr == "") {
			return nil, fmt.Errorf("%s: %s: lifecycle.replace_triggered_by: each element must be a resource reference, such as baseplate_data.hash, baseplate_data.hash[each.key] or baseplate_data.hash.output", e.Range(), inst)
		}
		if t.partial {
			return nil, fmt.Errorf("%s: %s: lifecycle.replace_triggered_by: reference a whole attribute (type.name.attr), not part of one", e.Range(), inst)
		}
		for _, a := range t.addresses(l.resources[t.typ][t.name]) {
			if a == inst {
				return nil, fmt.Errorf("%s: %s: lifecycle.replace_triggered_by can't reference the resource itself", e.Range(), inst)
			}
			refs = append(refs, provider.Ref{Address: a, Attr: t.attr})
		}
	}
	return refs, nil
}

func describeValue(v cty.Value) string {
	if v.IsNull() {
		return "null"
	}
	return v.Type().FriendlyName()
}
