package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/outpostgroupio/baseplate/internal/addr"
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
		{Type: "locals"},
		{Type: "moved"},
		{Type: "backend", LabelNames: []string{"type"}},
	},
}

// metaSchema pulls the resource meta-arguments (the for_each attribute and the
// lifecycle block) out of a resource body before hcldec decoding, so no
// resource has to declare them in its DecodeSpec.
var metaSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "for_each"}},
	Blocks:     []hcl.BlockHeaderSchema{{Type: "lifecycle"}},
}

// declaredResource records what Load knows about one resource block before
// decoding any bodies: whether it has for_each and, if so, its instance keys.
// References are checked against it (missing keys, unindexed for_each refs).
type declaredResource struct {
	keyed  bool
	keys   []string // sorted
	keySet map[string]bool
	values map[string]cty.Value // each.value per key
	body   hcl.Body             // the body with for_each and lifecycle stripped
	// preventDestroy is lifecycle.prevent_destroy; it applies to every instance.
	preventDestroy bool
}

// loader carries the per-Load state shared by the block handlers.
type loader struct {
	path      string
	resources map[string]map[string]*declaredResource // type -> name -> block
}

// Config is everything Load extracts from a config file.
type Config struct {
	Provider  ProviderConfig
	Resources []plan.DesiredResource
	Imports   []plan.ImportBlock
	// EmptyForEach lists the for_each resources (type.name) that declare zero
	// instances, so plan can warn that every instance in state is destroyed.
	EmptyForEach []string
	// PreventDestroy lists the resource blocks (type.name) that set
	// lifecycle.prevent_destroy = true, sorted. It includes for_each blocks
	// with zero instances, which still protect the instances left in state.
	PreventDestroy []string
	// Moved lists the expanded moved {} blocks (one per for_each instance), in
	// declaration order. plan.ApplyMoves applies them to state before planning.
	Moved []plan.MovedBlock
}

// Load is LoadConfig for callers that only need the provider config, the
// resources and the import blocks.
func Load(path string, p provider.Provider) (ProviderConfig, []plan.DesiredResource, []plan.ImportBlock, error) {
	cfg, err := LoadConfig(path, p)
	if err != nil {
		return ProviderConfig{}, nil, nil, err
	}
	return cfg.Provider, cfg.Resources, cfg.Imports, nil
}

// LoadConfig parses and evaluates a config file: locals, for_each expansion,
// references and import blocks.
func LoadConfig(path string, p provider.Provider) (*Config, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	content, diags := f.Body.Content(rootSchema)
	if diags.HasErrors() {
		return nil, diags
	}

	resTypes := p.Resources()
	l := &loader{path: path, resources: map[string]map[string]*declaredResource{}}

	// Index every resource block first, so locals and for_each expressions can
	// recognise (and reject) resource references, and so a reference can be
	// checked against its target's instance keys regardless of block order.
	for _, b := range content.Blocks {
		if b.Type != "resource" {
			continue
		}
		typ, name := b.Labels[0], b.Labels[1]
		if l.resources[typ][name] != nil {
			return nil, fmt.Errorf("%s: duplicate resource block %s.%s", path, typ, name)
		}
		if l.resources[typ] == nil {
			l.resources[typ] = map[string]*declaredResource{}
		}
		l.resources[typ][name] = &declaredResource{}
	}

	locals, err := l.evalLocals(content.Blocks)
	if err != nil {
		return nil, err
	}
	vars := buildRefObject(content.Blocks)
	vars["local"] = cty.ObjectVal(locals)
	vars["path"] = pathObject(path)
	ctx := &hcl.EvalContext{Variables: vars, Functions: functions()}

	var empty, protected []string
	// Expand for_each on every resource block. Keys must be known now, before
	// any body is decoded, because references are validated against them.
	for _, b := range content.Blocks {
		if b.Type != "resource" {
			continue
		}
		typ, name := b.Labels[0], b.Labels[1]
		fe, remain, d := b.Body.PartialContent(metaSchema)
		if d.HasErrors() {
			return nil, d
		}
		decl := l.resources[typ][name]
		decl.body = remain
		lc, err := l.decodeLifecycle(fe.Blocks, typ+"."+name)
		if err != nil {
			return nil, err
		}
		decl.preventDestroy = lc.preventDestroy
		if lc.preventDestroy {
			protected = append(protected, typ+"."+name)
		}
		if a, ok := fe.Attributes["for_each"]; ok {
			keys, values, err := l.evalForEach(a, ctx, typ+"."+name)
			if err != nil {
				return nil, err
			}
			decl.keyed, decl.keys, decl.values = true, keys, values
			if len(keys) == 0 {
				empty = append(empty, typ+"."+name)
			}
			decl.keySet = map[string]bool{}
			for _, k := range keys {
				decl.keySet[k] = true
			}
		} else if err := noEach(bodyTraversals(b.Body)); err != nil {
			return nil, err
		}
	}

	var pc ProviderConfig
	var out []plan.DesiredResource
	var imports []plan.ImportBlock
	var moved []plan.MovedBlock
	importSeen := map[string]bool{}
	for _, b := range content.Blocks {
		switch b.Type {
		case "provider":
			attrs, d := b.Body.JustAttributes()
			if d.HasErrors() {
				return nil, d
			}
			pc.Attributes = provider.Attributes{}
			for k, a := range attrs {
				if err := noEach(a.Expr.Variables()); err != nil {
					return nil, err
				}
				v, vd := a.Expr.Value(ctx)
				if vd.HasErrors() {
					return nil, vd
				}
				pc.Attributes[k] = v
			}
		case "resource":
			typ, name := b.Labels[0], b.Labels[1]
			r, ok := resTypes[typ]
			if !ok {
				return nil, fmt.Errorf("%s: unknown resource type %q", path, typ)
			}
			decl := l.resources[typ][name]
			keys := []string{""}
			if decl.keyed {
				keys = decl.keys
			}
			for _, key := range keys {
				ictx := ctx
				if decl.keyed {
					ictx = eachContext(ctx, key, decl.values[key])
				}
				dr, err := l.decodeInstance(b.Body, decl.body, r, typ, name, key, ictx)
				if err != nil {
					return nil, err
				}
				dr.PreventDestroy = decl.preventDestroy
				out = append(out, dr)
			}
		case "import":
			imps, err := l.expandImport(b, ctx)
			if err != nil {
				return nil, err
			}
			for _, imp := range imps {
				if importSeen[imp.Address] {
					return nil, fmt.Errorf("%s: duplicate import target %s", path, imp.Address)
				}
				importSeen[imp.Address] = true
			}
			imports = append(imports, imps...)
		case "moved":
			ms, err := l.expandMoved(b, ctx, resTypes)
			if err != nil {
				return nil, err
			}
			moved = append(moved, ms...)
		}
	}
	if err := l.checkMovedSet(moved); err != nil {
		return nil, err
	}
	sort.Strings(empty)
	sort.Strings(protected)
	return &Config{Provider: pc, Resources: out, Imports: imports, EmptyForEach: empty, PreventDestroy: protected, Moved: moved}, nil
}

// decodeInstance decodes one resource instance (the whole resource, for a
// block without for_each) in its eval context. body is the original block
// body (for reference analysis); remain has the for_each and lifecycle
// meta-arguments stripped (for decoding).
func (l *loader) decodeInstance(body, remain hcl.Body, r provider.Resource, typ, name, key string, ctx *hcl.EvalContext) (plan.DesiredResource, error) {
	inst := addr.Format(typ, name, key)
	// Attributes whose value is a reference to another resource carry an
	// unknown placeholder after decoding; move them out of literal Attributes
	// into Refs (resolved at apply time). 2-part resource refs (used purely as
	// a dependency) are dropped from literal attrs entirely.
	refAttrs, refNames, deps, err := l.bodyRefs(body, ctx, inst)
	if err != nil {
		return plan.DesiredResource{}, err
	}
	val, d := hcldec.Decode(remain, r.DecodeSpec(), ctx)
	if d.HasErrors() {
		return plan.DesiredResource{}, d
	}
	attrs := provider.Attributes{}
	for k, v := range val.AsValueMap() {
		attrs[k] = v
	}
	for k := range refNames {
		delete(attrs, k)
	}
	// hcldec decodes UNSET optional attributes as present-but-null. Keep
	// only attributes the user actually wrote in the HCL body, so unset
	// optional fields aren't treated as declared (which would make them
	// diff as null-vs-real-value forever). References were already removed
	// above; what remains must be genuine literals the user authored.
	written := bodyAttrNames(body)
	for k := range attrs {
		if !written[k] {
			delete(attrs, k)
		}
	}
	// After removing bare references, any remaining unknown attribute is a
	// reference used in an unsupported form (e.g. inside a string template
	// or a function call). Reject it loudly rather than silently dropping it.
	for k, v := range attrs {
		// IsWhollyKnown, not IsKnown: a reference nested inside a map/object
		// attribute (e.g. entries = { id = type.name.id }) leaves the container
		// known but an element unknown.
		if !v.IsWhollyKnown() {
			return plan.DesiredResource{}, fmt.Errorf("%s: %s: attribute %q references another resource in an unsupported way; use a bare reference like type.name.attr", l.path, inst, k)
		}
	}
	return plan.DesiredResource{
		Type: typ, Name: name, Key: key, Attributes: attrs,
		DependsOn: deps, Refs: refAttrs,
	}, nil
}

// expandImport turns one import block into one ImportBlock per for_each
// instance (or exactly one without for_each). `to` may index the target with a
// static key or any expression (e.g. [each.key]); `id` may use each.
func (l *loader) expandImport(b *hcl.Block, ctx *hcl.EvalContext) ([]plan.ImportBlock, error) {
	attrs, d := b.Body.JustAttributes()
	if d.HasErrors() {
		return nil, d
	}
	toAttr, ok := attrs["to"]
	if !ok {
		return nil, fmt.Errorf("%s: import block requires a `to` argument", l.path)
	}
	idAttr, ok := attrs["id"]
	if !ok {
		return nil, fmt.Errorf("%s: import block requires an `id` argument", l.path)
	}
	keys := []string{""}
	var values map[string]cty.Value
	fe, hasForEach := attrs["for_each"]
	if hasForEach {
		var err error
		if keys, values, err = l.evalForEach(fe, ctx, "import"); err != nil {
			return nil, err
		}
	} else if err := noEach(append(toAttr.Expr.Variables(), idAttr.Expr.Variables()...)); err != nil {
		return nil, err
	}
	var out []plan.ImportBlock
	for _, key := range keys {
		ictx := ctx
		if hasForEach {
			ictx = eachContext(ctx, key, values[key])
		}
		address, err := l.importTarget(toAttr.Expr, ictx)
		if err != nil {
			return nil, err
		}
		idv, vd := idAttr.Expr.Value(ictx)
		if vd.HasErrors() {
			return nil, vd
		}
		// Accept a number too: id maps decoded from JSON often hold numeric ids.
		if !idv.IsNull() && idv.IsKnown() && idv.Type() == cty.Number {
			idv, _ = convert.Convert(idv, cty.String)
		}
		if idv.IsNull() || !idv.IsKnown() || idv.Type() != cty.String {
			return nil, fmt.Errorf("%s: import %s: `id` must be a string", l.path, address)
		}
		out = append(out, plan.ImportBlock{Address: address, ID: idv.AsString()})
	}
	return out, nil
}

// importTarget resolves an import block's `to` to an instance address. The
// target needn't be declared (plan --generate-config handles undeclared ones),
// but when it is, its for_each-ness must match the address shape.
func (l *loader) importTarget(expr hcl.Expression, ctx *hcl.EvalContext) (string, error) {
	typ, name, key, hasKey, err := l.addressExpr(expr, ctx, "import `to`")
	if err != nil {
		return "", err
	}
	if decl := l.resources[typ][name]; decl != nil {
		base := typ + "." + name
		if decl.keyed && !hasKey {
			return "", fmt.Errorf("%s: import `to`: %s has for_each; import into a specific instance, e.g. to = %s[each.key]", l.path, base, base)
		}
		if !decl.keyed && hasKey {
			return "", fmt.Errorf("%s: import `to`: %s does not have for_each, so it has no instance %s", l.path, base, addr.Quote(key))
		}
	}
	return addr.Format(typ, name, key), nil
}

// addressExpr reads a resource address written as an expression: type.name,
// type.name["key"], or type.name[expr] with expr evaluated in ctx (e.g.
// [each.key]). The address itself is never evaluated, so it needn't be
// declared. what prefixes errors (e.g. "import `to`").
func (l *loader) addressExpr(expr hcl.Expression, ctx *hcl.EvalContext, what string) (typ, name, key string, hasKey bool, err error) {
	bad := fmt.Errorf(`%s: %s must be a resource address like type.name or type.name["key"]`, l.path, what)
	switch e := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		tr := e.Traversal
		n, ok := traversalIndexName(tr, 1)
		if !ok || len(tr) > 3 {
			return "", "", "", false, bad
		}
		typ, name = tr.RootName(), n
		if len(tr) == 3 {
			idx, ok := tr[2].(hcl.TraverseIndex)
			if !ok {
				return "", "", "", false, bad
			}
			if idx.Key.Type() != cty.String {
				return "", "", "", false, fmt.Errorf("%s: %s: instance key must be a string", l.path, what)
			}
			key, hasKey = idx.Key.AsString(), true
		}
	case *hclsyntax.IndexExpr:
		st, ok := e.Collection.(*hclsyntax.ScopeTraversalExpr)
		if !ok || len(st.Traversal) != 2 {
			return "", "", "", false, bad
		}
		n, ok := traversalIndexName(st.Traversal, 1)
		if !ok {
			return "", "", "", false, bad
		}
		typ, name = st.Traversal.RootName(), n
		k, err := l.evalKey(e.Key, ctx, what)
		if err != nil {
			return "", "", "", false, err
		}
		key, hasKey = k, true
	default:
		return "", "", "", false, bad
	}
	if hasKey && key == "" {
		return "", "", "", false, fmt.Errorf("%s: %s: instance key must not be empty", l.path, what)
	}
	return typ, name, key, hasKey, nil
}

// evalForEach evaluates a for_each expression and returns its instance keys
// (sorted lexically) and each key's each.value. The value must be known at load
// time: a map/object (keys -> values) or a set of strings (each.key ==
// each.value). Lists are rejected — order isn't identity, and silently
// de-duplicating would hide mistakes.
func (l *loader) evalForEach(a *hcl.Attribute, ctx *hcl.EvalContext, what string) ([]string, map[string]cty.Value, error) {
	trs := a.Expr.Variables()
	if err := noEach(trs); err != nil {
		return nil, nil, err
	}
	for _, tr := range trs {
		if l.resources[tr.RootName()] != nil {
			return nil, nil, fmt.Errorf("%s: %s: for_each value depends on %s, which is only known at apply time", l.path, what, traversalString(tr))
		}
	}
	v, d := a.Expr.Value(ctx)
	if d.HasErrors() {
		return nil, nil, d
	}
	if v.IsNull() {
		return nil, nil, fmt.Errorf("%s: %s: for_each must not be null", l.path, what)
	}
	if !v.IsWhollyKnown() {
		return nil, nil, fmt.Errorf("%s: %s: for_each value must be known when the config loads", l.path, what)
	}
	ty := v.Type()
	values := map[string]cty.Value{}
	switch {
	case ty.IsMapType() || ty.IsObjectType():
		for it := v.ElementIterator(); it.Next(); {
			k, ev := it.Element()
			values[k.AsString()] = ev
		}
	case ty.IsSetType():
		// An empty set may be untyped (toset([]) is a set of dynamic).
		if !ty.ElementType().Equals(cty.String) && v.LengthInt() > 0 {
			return nil, nil, fmt.Errorf("%s: %s: for_each must be a map or a set of strings; got %s", l.path, what, ty.FriendlyName())
		}
		for it := v.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			if ev.IsNull() {
				return nil, nil, fmt.Errorf("%s: %s: for_each set must not contain null", l.path, what)
			}
			values[ev.AsString()] = ev
		}
	case ty.IsListType() || ty.IsTupleType():
		return nil, nil, fmt.Errorf("%s: %s: for_each must be a map or a set of strings; got list — wrap it in toset(...)", l.path, what)
	default:
		return nil, nil, fmt.Errorf("%s: %s: for_each must be a map or a set of strings; got %s", l.path, what, ty.FriendlyName())
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		if k == "" {
			return nil, nil, fmt.Errorf("%s: %s: for_each keys must be non-empty strings", l.path, what)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, values, nil
}

// eachContext returns a child of ctx in which `each` is bound to one instance.
func eachContext(ctx *hcl.EvalContext, key string, value cty.Value) *hcl.EvalContext {
	child := ctx.NewChild()
	child.Variables = map[string]cty.Value{
		"each": cty.ObjectVal(map[string]cty.Value{"key": cty.StringVal(key), "value": value}),
	}
	return child
}

// noEach rejects any `each.*` traversal; used for blocks without for_each.
func noEach(trs []hcl.Traversal) error {
	for _, tr := range trs {
		if tr.RootName() == "each" {
			return fmt.Errorf(`%s: "each" is only available in a block that sets for_each`, tr.SourceRange())
		}
	}
	return nil
}

// pathObject is the `path` variable: path.config is the config file's
// directory (forward slashes), so a config can address files relative to
// itself regardless of the working directory.
func pathObject(cfgPath string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"config": cty.StringVal(filepath.ToSlash(filepath.Dir(cfgPath))),
	})
}

// buildRefObject makes each resource block referenceable in expressions as
// `<type>.<name>`. Each reference is an UNKNOWN dynamic value: attribute access
// like `roblox_asset.gem_icon.id` (or indexing, for a for_each resource) yields
// another unknown, which decodes cleanly to any target type. The real value is
// unknown until apply — config only needs to record the Ref (see bodyRefs);
// the unknown is discarded.
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

// bodyAttrNames returns the set of attribute names the user actually wrote in
// the resource body (as opposed to optional attributes hcldec defaults to null).
// The for_each meta-argument is not a resource attribute and is skipped.
func bodyAttrNames(body hcl.Body) map[string]bool {
	out := map[string]bool{}
	if syn, ok := body.(*hclsyntax.Body); ok {
		for name := range syn.Attributes {
			if name != "for_each" {
				out[name] = true
			}
		}
	}
	return out
}

// bodyTraversals returns every variable traversal used in a body's attributes
// and nested blocks.
func bodyTraversals(body hcl.Body) []hcl.Traversal {
	syn, ok := body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	var out []hcl.Traversal
	for _, a := range syn.Attributes {
		out = append(out, a.Expr.Variables()...)
	}
	for _, b := range syn.Blocks {
		out = append(out, bodyTraversals(b.Body)...)
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

// traversalString renders a traversal for error messages, e.g.
// roblox_experience.main.id or roblox_asset.sprites["k"].id.
func traversalString(tr hcl.Traversal) string {
	var b strings.Builder
	for _, step := range tr {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			b.WriteString(s.Name)
		case hcl.TraverseAttr:
			b.WriteString("." + s.Name)
		case hcl.TraverseIndex:
			if s.Key.Type() == cty.String {
				b.WriteString("[" + addr.Quote(s.Key.AsString()) + "]")
			} else {
				b.WriteString("[…]")
			}
		}
	}
	return b.String()
}
