package config

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
)

// OutputBlock is one output {} block. Its value is evaluated after the
// resources it references are known (EvalOutputs), not at load.
type OutputBlock struct {
	Name        string
	Description string
	Sensitive   bool
	value       hcl.Expression
}

var outputSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "value", Required: true},
		{Name: "description"},
		{Name: "sensitive"},
	},
}

// decodeOutput reads an output block. value may use anything a resource
// attribute can except each; it is evaluated here only to report errors such
// as an undeclared variable early (references are still unknown).
func (l *loader) decodeOutput(b *hcl.Block, ctx *hcl.EvalContext) (OutputBlock, error) {
	name := b.Labels[0]
	if !hclsyntax.ValidIdentifier(name) {
		return OutputBlock{}, fmt.Errorf("%s: output name %q must be a valid identifier", b.LabelRanges[0], name)
	}
	content, d := b.Body.Content(outputSchema)
	if d.HasErrors() {
		return OutputBlock{}, d
	}
	o := OutputBlock{Name: name, value: content.Attributes["value"].Expr}
	if err := noEach(o.value.Variables()); err != nil {
		return OutputBlock{}, err
	}
	v, d := o.value.Value(ctx)
	if d.HasErrors() {
		return OutputBlock{}, d
	}
	if a, ok := content.Attributes["description"]; ok {
		v, d := a.Expr.Value(nil)
		if d.HasErrors() || v.IsNull() || v.Type() != cty.String {
			return OutputBlock{}, fmt.Errorf("%s: output %q: description must be a literal string", a.Range, name)
		}
		o.Description = v.AsString()
	}
	if a, ok := content.Attributes["sensitive"]; ok {
		v, d := a.Expr.Value(nil)
		if d.HasErrors() || v.IsNull() || v.Type() != cty.Bool {
			return OutputBlock{}, fmt.Errorf("%s: output %q: sensitive must be a literal true or false", a.Range, name)
		}
		o.Sensitive = v.True()
	}
	if err := o.checkSensitive(v); err != nil {
		return OutputBlock{}, err
	}
	return o, nil
}

// checkSensitive rejects a sensitive value (plan.SensitiveMark) in an
// output that doesn't say sensitive = true, as Terraform does, so a secret
// isn't published in plain sight by accident.
func (o OutputBlock) checkSensitive(v cty.Value) error {
	if o.Sensitive || !v.ContainsMarked() {
		return nil
	}
	return fmt.Errorf("output %q refers to sensitive values; set sensitive = true in it, or use nonsensitive() if the value may be shown", o.Name)
}

// EvalOutputs evaluates the output blocks. instances maps an instance address
// to its value (an object of its attributes and id); an instance it lacks,
// e.g. one not created yet, is unknown, and so is any output that uses it.
func (c *Config) EvalOutputs(instances map[string]cty.Value) (map[string]plan.OutputValue, error) {
	if len(c.Outputs) == 0 {
		return nil, nil
	}
	vars := map[string]cty.Value{}
	for k, v := range c.vars {
		vars[k] = v
	}
	for typ, names := range c.resources {
		byName := map[string]cty.Value{}
		for name, decl := range names {
			if !decl.keyed {
				byName[name] = instanceOr(instances, addr.Format(typ, name, ""))
				continue
			}
			byKey := map[string]cty.Value{}
			for _, k := range decl.keys {
				byKey[k] = instanceOr(instances, addr.Format(typ, name, k))
			}
			byName[name] = cty.ObjectVal(byKey)
		}
		vars[typ] = cty.ObjectVal(byName)
	}
	ctx := &hcl.EvalContext{Variables: vars, Functions: functions()}
	out := make(map[string]plan.OutputValue, len(c.Outputs))
	for _, o := range c.Outputs {
		v, d := o.value.Value(ctx)
		if d.HasErrors() {
			return nil, fmt.Errorf("output %q: %w", o.Name, d)
		}
		if err := o.checkSensitive(v); err != nil {
			return nil, err
		}
		v, _ = v.UnmarkDeep()
		out[o.Name] = plan.OutputValue{Value: v, Sensitive: o.Sensitive}
	}
	return out, nil
}

func instanceOr(instances map[string]cty.Value, address string) cty.Value {
	if v, ok := instances[address]; ok {
		return v
	}
	return cty.DynamicVal
}
