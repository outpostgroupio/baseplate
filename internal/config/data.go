package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// remoteStateType is the one data source type: Terraform's
// terraform_remote_state under Baseplate's name.
const remoteStateType = "baseplate_remote_state"

// RemoteStateSource is a baseplate_remote_state data source: the backend
// whose state it reads, configured as that backend's block would be.
type RemoteStateSource struct {
	// Name is the data block's name (data.baseplate_remote_state.<name>).
	Name    string
	Backend string
	Config  map[string]cty.Value
}

// RemoteStateReader returns the output values recorded in the state src
// names, or nil when that state doesn't exist yet. The value of a sensitive
// output is marked plan.SensitiveMark.
type RemoteStateReader func(src RemoteStateSource) (map[string]cty.Value, error)

// LoadOptions tunes LoadConfigWith.
type LoadOptions struct {
	// RemoteState reads the data sources' states. Without it, as in
	// validate, their outputs are unknown, and so is every value that uses
	// them.
	RemoteState RemoteStateReader
}

var remoteStateSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "backend", Required: true},
		{Name: "config"},
		{Name: "defaults"},
	},
}

// evalData reads every data block and returns the `data` variable:
// data.baseplate_remote_state.<name>.outputs. A data block's arguments may use
// path.config, env() and the other functions, but not locals, resources or
// other data sources, since they are read before anything else is evaluated.
// known is false when there was no reader, so the outputs are unknown.
func (l *loader) evalData(blocks hcl.Blocks, read RemoteStateReader) (data cty.Value, known bool, err error) {
	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{"path": pathObject(l.path)},
		Functions: functions(),
	}
	byName := map[string]cty.Value{}
	at := map[string]hcl.Range{}
	for _, b := range blocks {
		if b.Type != "data" {
			continue
		}
		typ, name := b.Labels[0], b.Labels[1]
		if typ != remoteStateType {
			return cty.NilVal, false, fmt.Errorf("%s: unknown data source type %q; the only one is %s", b.LabelRanges[0], typ, remoteStateType)
		}
		if prev, dup := at[name]; dup {
			return cty.NilVal, false, fmt.Errorf("%s: duplicate data block %s.%s (also defined at %s)", b.DefRange, typ, name, prev)
		}
		at[name] = b.DefRange
		content, d := b.Body.Content(remoteStateSchema)
		if d.HasErrors() {
			return cty.NilVal, false, d
		}
		vals := map[string]cty.Value{}
		for k, a := range content.Attributes {
			for _, tr := range a.Expr.Variables() {
				if tr.RootName() != "path" {
					return cty.NilVal, false, fmt.Errorf("%s: data.%s.%s: %s can't be used here; a data source's arguments may use path.config, env() and functions only", tr.SourceRange(), typ, name, traversalString(tr))
				}
			}
			v, d := a.Expr.Value(ctx)
			if d.HasErrors() {
				return cty.NilVal, false, d
			}
			vals[k] = v
		}
		src := RemoteStateSource{Name: name}
		if v := vals["backend"]; v.IsNull() || v.Type() != cty.String {
			return cty.NilVal, false, fmt.Errorf("%s: data.%s.%s: backend must be a string such as \"http\"", b.DefRange, typ, name)
		} else {
			src.Backend = v.AsString()
		}
		if src.Config, err = objectAttrs(vals["config"], "config"); err != nil {
			return cty.NilVal, false, fmt.Errorf("%s: data.%s.%s: %w", b.DefRange, typ, name, err)
		}
		defaults, err := objectAttrs(vals["defaults"], "defaults")
		if err != nil {
			return cty.NilVal, false, fmt.Errorf("%s: data.%s.%s: %w", b.DefRange, typ, name, err)
		}
		outputs := cty.DynamicVal
		if read != nil {
			got, err := read(src)
			if err != nil {
				return cty.NilVal, false, fmt.Errorf("data.%s.%s: %w", typ, name, err)
			}
			merged := map[string]cty.Value{}
			for k, v := range defaults {
				merged[k] = v
			}
			for k, v := range got {
				merged[k] = v
			}
			outputs = cty.ObjectVal(merged)
		}
		byName[name] = cty.ObjectVal(map[string]cty.Value{"outputs": outputs})
	}
	if len(byName) == 0 {
		return cty.EmptyObjectVal, true, nil
	}
	return cty.ObjectVal(map[string]cty.Value{remoteStateType: cty.ObjectVal(byName)}), read != nil, nil
}

// objectAttrs returns the attributes of an object or map value (absent: none).
func objectAttrs(v cty.Value, what string) (map[string]cty.Value, error) {
	if v == cty.NilVal || v.IsNull() {
		return nil, nil
	}
	if !v.IsWhollyKnown() || !(v.Type().IsObjectType() || v.Type().IsMapType()) {
		return nil, fmt.Errorf("%s must be an object, e.g. { address = \"...\" }", what)
	}
	return v.AsValueMap(), nil
}

// checkDataRefs reports a reference to an undeclared data source, or to an
// output its state doesn't have, listing the ones there are. HCL's own error
// for these ("This object does not have an attribute named ...") doesn't.
func (l *loader) checkDataRefs(data cty.Value, trs []hcl.Traversal) error {
	for _, tr := range trs {
		if tr.RootName() != "data" {
			continue
		}
		typ, ok := traversalIndexName(tr, 1)
		if !ok || typ != remoteStateType {
			return fmt.Errorf("%s: %s: a data source is referenced as data.%s.<name>.outputs.<output>", tr.SourceRange(), traversalString(tr), remoteStateType)
		}
		name, ok := traversalIndexName(tr, 2)
		if !ok {
			continue
		}
		if !data.Type().IsObjectType() || !data.Type().HasAttribute(typ) || !data.GetAttr(typ).Type().HasAttribute(name) {
			return fmt.Errorf("%s: %s: no data block %s.%s is declared", tr.SourceRange(), traversalString(tr), typ, name)
		}
		if attr, ok := traversalIndexName(tr, 3); ok && attr != "outputs" {
			return fmt.Errorf("%s: %s: a data source's only attribute is outputs", tr.SourceRange(), traversalString(tr))
		}
		outputs := data.GetAttr(typ).GetAttr(name).GetAttr("outputs")
		out, ok := traversalIndexName(tr, 4)
		if !ok || !outputs.IsKnown() || outputs.Type().HasAttribute(out) {
			continue
		}
		names := make([]string, 0)
		for k := range outputs.Type().AttributeTypes() {
			names = append(names, k)
		}
		sort.Strings(names)
		have := "it has none"
		if len(names) > 0 {
			have = "it has: " + strings.Join(names, ", ")
		}
		return fmt.Errorf("%s: %s: the state read by data.%s.%s has no output %q (%s)", tr.SourceRange(), traversalString(tr), typ, name, out, have)
	}
	return nil
}

// usesData reports whether expr may depend on a data source: it references
// one, or a local (which may), and no resource.
func usesData(expr hcl.Expression, resources map[string]map[string]*declaredResource) bool {
	data := false
	for _, tr := range expr.Variables() {
		switch r := tr.RootName(); {
		case r == "data" || r == "local":
			data = true
		case resources[r] != nil:
			return false
		}
	}
	return data
}

// attrUsesData reports whether a resource body's attribute name references a
// data source, directly or through a local. A local is followed by name only,
// so any local that uses a data source counts.
func attrUsesData(body hcl.Body, name string) bool {
	syn, ok := body.(*hclsyntax.Body)
	if !ok {
		return false
	}
	a, ok := syn.Attributes[name]
	if !ok {
		return false
	}
	for _, tr := range a.Expr.Variables() {
		if r := tr.RootName(); r == "data" || r == "local" {
			return true
		}
	}
	return false
}
