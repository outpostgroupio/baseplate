package config

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
)

type ProviderConfig struct {
	Attributes provider.Attributes
}

var rootSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "provider", LabelNames: []string{"name"}},
		{Type: "resource", LabelNames: []string{"type", "name"}},
	},
}

func Load(path string, p provider.Provider) (ProviderConfig, []plan.DesiredResource, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return ProviderConfig{}, nil, err
	}
	f, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
	if diags.HasErrors() {
		return ProviderConfig{}, nil, diags
	}
	content, diags := f.Body.Content(rootSchema)
	if diags.HasErrors() {
		return ProviderConfig{}, nil, diags
	}

	resTypes := p.Resources()

	ctx := &hcl.EvalContext{
		Variables: buildRefObject(content.Blocks),
		Functions: map[string]function.Function{"env": envFunc()},
	}

	var pc ProviderConfig
	var out []plan.DesiredResource
	for _, b := range content.Blocks {
		switch b.Type {
		case "provider":
			attrs, d := b.Body.JustAttributes()
			if d.HasErrors() {
				return pc, nil, d
			}
			pc.Attributes = provider.Attributes{}
			for k, a := range attrs {
				v, vd := a.Expr.Value(ctx)
				if vd.HasErrors() {
					return pc, nil, vd
				}
				pc.Attributes[k] = v
			}
		case "resource":
			typ, name := b.Labels[0], b.Labels[1]
			r, ok := resTypes[typ]
			if !ok {
				return pc, nil, fmt.Errorf("%s: unknown resource type %q", path, typ)
			}
			val, d := hcldec.Decode(b.Body, r.DecodeSpec(), ctx)
			if d.HasErrors() {
				return pc, nil, d
			}
			attrs := provider.Attributes{}
			for k, v := range val.AsValueMap() {
				attrs[k] = v
			}
			out = append(out, plan.DesiredResource{
				Type: typ, Name: name, Attributes: attrs,
				DependsOn: extractDeps(b.Body),
			})
		}
	}
	return pc, out, nil
}

// buildRefObject makes each resource block referenceable in expressions as
// `<type>.<name>`, evaluating to the address string (Phase 0 reference model).
func buildRefObject(blocks hcl.Blocks) map[string]cty.Value {
	byType := map[string]map[string]cty.Value{}
	for _, b := range blocks {
		if b.Type != "resource" {
			continue
		}
		typ, name := b.Labels[0], b.Labels[1]
		if byType[typ] == nil {
			byType[typ] = map[string]cty.Value{}
		}
		byType[typ][name] = cty.StringVal(typ + "." + name)
	}
	out := map[string]cty.Value{}
	for typ, names := range byType {
		out[typ] = cty.ObjectVal(names)
	}
	return out
}

// extractDeps finds `<type>.<name>` traversals used in a resource body and
// returns them as dependency addresses.
func extractDeps(body hcl.Body) []string {
	syn, ok := body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var deps []string
	for _, attr := range syn.Attributes {
		for _, tr := range attr.Expr.Variables() {
			name, ok := traversalName(tr)
			if !ok {
				continue
			}
			addr := tr.RootName() + "." + name
			if !seen[addr] {
				seen[addr] = true
				deps = append(deps, addr)
			}
		}
	}
	return deps
}

func traversalName(tr hcl.Traversal) (string, bool) {
	if len(tr) < 2 {
		return "", false
	}
	if a, ok := tr[1].(hcl.TraverseAttr); ok {
		return a.Name, true
	}
	return "", false
}

func envFunc() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "name", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.StringVal(os.Getenv(args[0].AsString())), nil
		},
	})
}
