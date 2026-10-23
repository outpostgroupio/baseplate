package config

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

type ProviderConfig struct {
	Attributes provider.Attributes
}

type BackendConfig struct {
	Type       string
	Attributes map[string]cty.Value
}

var backendSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{{Type: "backend", LabelNames: []string{"type"}}},
}

// LoadBackend returns the single `backend` block from the config file, or nil if
// there is none or the file does not exist (callers then use the local backend).
func LoadBackend(path string) (*BackendConfig, error) {
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	content, _, diags := f.Body.PartialContent(backendSchema)
	if diags.HasErrors() {
		return nil, diags
	}
	var blocks []*hcl.Block
	for _, b := range content.Blocks {
		if b.Type == "backend" {
			blocks = append(blocks, b)
		}
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	if len(blocks) > 1 {
		return nil, fmt.Errorf("%s: only one backend block is allowed", path)
	}
	b := blocks[0]
	ctx := &hcl.EvalContext{Functions: map[string]function.Function{"env": envFunc()}}
	attrs, d := b.Body.JustAttributes()
	if d.HasErrors() {
		return nil, d
	}
	out := &BackendConfig{Type: b.Labels[0], Attributes: map[string]cty.Value{}}
	for k, a := range attrs {
		v, vd := a.Expr.Value(ctx)
		if vd.HasErrors() {
			return nil, vd
		}
		out.Attributes[k] = v
	}
	return out, nil
}

var rootSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "provider", LabelNames: []string{"name"}},
		{Type: "resource", LabelNames: []string{"type", "name"}},
		{Type: "import"},
		{Type: "backend", LabelNames: []string{"type"}},
	},
}

func Load(path string, p provider.Provider) (ProviderConfig, []plan.DesiredResource, []plan.ImportBlock, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return ProviderConfig{}, nil, nil, err
	}
	f, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
	if diags.HasErrors() {
		return ProviderConfig{}, nil, nil, diags
	}
	content, diags := f.Body.Content(rootSchema)
	if diags.HasErrors() {
		return ProviderConfig{}, nil, nil, diags
	}

	resTypes := p.Resources()

	ctx := &hcl.EvalContext{
		Variables: buildRefObject(content.Blocks),
		Functions: map[string]function.Function{"env": envFunc()},
	}

	var pc ProviderConfig
	var out []plan.DesiredResource
	var imports []plan.ImportBlock
	for _, b := range content.Blocks {
		switch b.Type {
		case "provider":
			attrs, d := b.Body.JustAttributes()
			if d.HasErrors() {
				return pc, nil, nil, d
			}
			pc.Attributes = provider.Attributes{}
			for k, a := range attrs {
				v, vd := a.Expr.Value(ctx)
				if vd.HasErrors() {
					return pc, nil, nil, vd
				}
				pc.Attributes[k] = v
			}
		case "resource":
			typ, name := b.Labels[0], b.Labels[1]
			r, ok := resTypes[typ]
			if !ok {
				return pc, nil, nil, fmt.Errorf("%s: unknown resource type %q", path, typ)
			}
			val, d := hcldec.Decode(b.Body, r.DecodeSpec(), ctx)
			if d.HasErrors() {
				return pc, nil, nil, d
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
			// hcldec decodes UNSET optional attributes as present-but-null. Keep
			// only attributes the user actually wrote in the HCL body, so unset
			// optional fields aren't treated as declared (which would make them
			// diff as null-vs-real-value forever). References were already removed
			// above; what remains must be genuine literals the user authored.
			written := bodyAttrNames(b.Body)
			for k := range attrs {
				if !written[k] {
					delete(attrs, k)
				}
			}
			// After removing bare references, any remaining unknown attribute is a
			// reference used in an unsupported form (e.g. inside a string template
			// or a function call). Reject it loudly rather than silently dropping it.
			for k, v := range attrs {
				if !v.IsKnown() {
					return pc, nil, nil, fmt.Errorf("%s: %s.%s: attribute %q references another resource in an unsupported way; use a bare reference like type.name.attr", path, typ, name, k)
				}
			}
			out = append(out, plan.DesiredResource{
				Type: typ, Name: name, Attributes: attrs,
				DependsOn: extractDeps(b.Body), Refs: refAttrs,
			})
		case "import":
			attrs, d := b.Body.JustAttributes()
			if d.HasErrors() {
				return pc, nil, nil, d
			}
			toAttr, ok := attrs["to"]
			if !ok {
				return pc, nil, nil, fmt.Errorf("%s: import block requires a `to` argument", path)
			}
			addr, ok := importAddress(toAttr.Expr)
			if !ok {
				return pc, nil, nil, fmt.Errorf("%s: import `to` must be a resource address like type.name", path)
			}
			idAttr, ok := attrs["id"]
			if !ok {
				return pc, nil, nil, fmt.Errorf("%s: import block requires an `id` argument", path)
			}
			idv, vd := idAttr.Expr.Value(ctx)
			if vd.HasErrors() {
				return pc, nil, nil, vd
			}
			if idv.IsNull() || idv.Type() != cty.String {
				return pc, nil, nil, fmt.Errorf("%s: import `id` must be a string", path)
			}
			imports = append(imports, plan.ImportBlock{Address: addr, ID: idv.AsString()})
		}
	}
	return pc, out, imports, nil
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

// bodyAttrNames returns the set of attribute names the user actually wrote in
// the resource body (as opposed to optional attributes hcldec defaults to null).
func bodyAttrNames(body hcl.Body) map[string]bool {
	out := map[string]bool{}
	if syn, ok := body.(*hclsyntax.Body); ok {
		for name := range syn.Attributes {
			out[name] = true
		}
	}
	return out
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

// importAddress extracts a "<type>.<name>" address from an import block's `to`
// expression, which must be a bare 2-part resource-reference traversal.
func importAddress(expr hcl.Expression) (string, bool) {
	te, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok {
		return "", false
	}
	name, ok := traversalName(te.Traversal)
	if !ok {
		return "", false
	}
	return te.Traversal.RootName() + "." + name, true
}
