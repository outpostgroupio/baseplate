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
			// Attributes whose value is a reference to another resource carry an
			// unknown placeholder here; move them out of literal Attributes into
			// Refs (resolved at apply time). 2-part resource refs (used purely as
			// a dependency) are dropped from literal attrs entirely.
			refAttrs, refNames := resourceRefs(b.Body)
			for k := range refNames {
				delete(attrs, k)
			}
			out = append(out, plan.DesiredResource{
				Type: typ, Name: name, Attributes: attrs,
				DependsOn: extractDeps(b.Body), Refs: refAttrs,
			})
		}
	}
	return pc, out, nil
}

// buildRefObject makes each resource block referenceable in expressions as
// `<type>.<name>`. Each reference is an UNKNOWN dynamic value: attribute access
// like `roblox_asset.gem_icon.id` yields another unknown, which decodes cleanly
// to any target type. The real value is unknown until apply — config only needs
// to record the Ref (see resourceRefs); the unknown is discarded.
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
		byType[typ][name] = cty.DynamicVal
	}
	out := map[string]cty.Value{}
	for typ, names := range byType {
		out[typ] = cty.ObjectVal(names)
	}
	return out
}

// resourceRefs inspects a resource body and classifies attributes whose value
// is a single reference to another resource. It returns:
//   - refAttrs: attr name -> Ref, for 3-part references `<type>.<name>.<attr>`
//     (resolved at apply time).
//   - refNames: the set of ALL attr names that are pure resource references
//     (2-part `<type>.<name>` or 3-part), so the caller drops them from literal
//     attributes.
func resourceRefs(body hcl.Body) (map[string]provider.Ref, map[string]bool) {
	refAttrs := map[string]provider.Ref{}
	refNames := map[string]bool{}
	syn, ok := body.(*hclsyntax.Body)
	if !ok {
		return refAttrs, refNames
	}
	for name, attr := range syn.Attributes {
		te, ok := attr.Expr.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			continue
		}
		tr := te.Traversal
		resName, ok := traversalIndexName(tr, 1)
		if !ok {
			continue
		}
		refNames[name] = true
		if attrName, ok := traversalIndexName(tr, 2); ok {
			refAttrs[name] = provider.Ref{Address: tr.RootName() + "." + resName, Attr: attrName}
		}
	}
	return refAttrs, refNames
}

func traversalIndexName(tr hcl.Traversal, i int) (string, bool) {
	if len(tr) <= i {
		return "", false
	}
	if a, ok := tr[i].(hcl.TraverseAttr); ok {
		return a.Name, true
	}
	return "", false
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
