package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

// refTarget is an expression resolved as a reference to a declared resource.
type refTarget struct {
	typ, name string
	key       string // instance key; "" for a singular resource or when all is set
	all       bool   // bare reference to a for_each resource: every instance
	attr      string // referenced attribute; "" for a dependency-only reference
}

// addresses returns the instance address(es) the reference depends on.
func (t refTarget) addresses(decl *declaredResource) []string {
	if !t.all {
		return []string{addr.Format(t.typ, t.name, t.key)}
	}
	out := make([]string, len(decl.keys))
	for i, k := range decl.keys {
		out[i] = addr.Format(t.typ, t.name, k)
	}
	return out
}

// dynamicKeyError marks an index expression whose key can't be evaluated in
// the current context (e.g. a for-expression variable). Dependency collection
// falls back to "every instance" for it; anywhere else it is a real error.
type dynamicKeyError struct{ msg string }

func (e *dynamicKeyError) Error() string { return e.msg }

// bodyRefs inspects a resource body in one instance's eval context and returns:
//   - refAttrs: attr name -> Ref, for attributes whose whole value is a single
//     reference to another resource's attribute (`type.name.attr`,
//     `type.name["k"].attr`, `type.name[expr].attr`), resolved at apply time.
//   - refNames: every attr name whose value is a pure resource reference
//     (including dependency-only `type.name` / `type.name["k"]`), which the
//     caller drops from literal attributes.
//   - deps: the sorted instance addresses referenced anywhere in the body. A
//     bare reference to a for_each resource depends on all of its instances.
func (l *loader) bodyRefs(body hcl.Body, ctx *hcl.EvalContext, inst string) (map[string]provider.Ref, map[string]bool, []string, error) {
	refAttrs := map[string]provider.Ref{}
	refNames := map[string]bool{}
	syn, ok := body.(*hclsyntax.Body)
	if !ok {
		return refAttrs, refNames, nil, nil
	}
	seen := map[string]bool{}
	if err := l.collectDeps(syn, ctx, inst, seen); err != nil {
		return nil, nil, nil, err
	}
	for name, attr := range syn.Attributes {
		if name == "for_each" {
			continue
		}
		t, ok, err := l.resolveRef(attr.Expr, ctx)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %s: attribute %q: %w", l.path, inst, name, err)
		}
		if !ok {
			continue
		}
		refNames[name] = true
		if t.attr != "" {
			refAttrs[name] = provider.Ref{Address: addr.Format(t.typ, t.name, t.key), Attr: t.attr}
		}
	}
	deps := make([]string, 0, len(seen))
	for a := range seen {
		deps = append(deps, a)
	}
	sort.Strings(deps)
	if len(deps) == 0 {
		deps = nil
	}
	return refAttrs, refNames, deps, nil
}

// collectDeps walks every expression in a body (attributes and nested blocks)
// and records the instance addresses of all resource references in seen.
func (l *loader) collectDeps(syn *hclsyntax.Body, ctx *hcl.EvalContext, inst string, seen map[string]bool) error {
	for name, attr := range syn.Attributes {
		if name == "for_each" {
			continue
		}
		// An IndexExpr's collection is itself a ScopeTraversalExpr; once the
		// index is handled, skip the collection so it doesn't also count as a
		// bare (all-instances) reference.
		handled := map[hclsyntax.Node]bool{}
		var walkErr error
		hclsyntax.VisitAll(attr.Expr, func(n hclsyntax.Node) hcl.Diagnostics {
			if walkErr != nil || handled[n] {
				return nil
			}
			var t refTarget
			var ok bool
			var err error
			switch e := n.(type) {
			case *hclsyntax.IndexExpr:
				st, isTr := e.Collection.(*hclsyntax.ScopeTraversalExpr)
				if !isTr || l.declared(st.Traversal) == nil {
					return nil
				}
				handled[st] = true
				t, ok, err = l.resolveRef(e, ctx)
				var dk *dynamicKeyError
				if errors.As(err, &dk) {
					t, ok, err = refTarget{typ: st.Traversal.RootName(), name: st.Traversal[1].(hcl.TraverseAttr).Name, all: true}, true, nil
				}
			case *hclsyntax.ScopeTraversalExpr:
				t, ok, err = l.resolveRef(e, ctx)
			default:
				return nil
			}
			if err != nil {
				walkErr = fmt.Errorf("%s: %s: attribute %q: %w", l.path, inst, name, err)
				return nil
			}
			if ok {
				for _, a := range t.addresses(l.resources[t.typ][t.name]) {
					seen[a] = true
				}
			}
			return nil
		})
		if walkErr != nil {
			return walkErr
		}
	}
	for _, b := range syn.Blocks {
		if err := l.collectDeps(b.Body, ctx, inst, seen); err != nil {
			return err
		}
	}
	return nil
}

// declared returns the resource block a traversal's first two steps name, or
// nil when the traversal isn't a resource reference.
func (l *loader) declared(tr hcl.Traversal) *declaredResource {
	name, ok := traversalIndexName(tr, 1)
	if !ok {
		return nil
	}
	return l.resources[tr.RootName()][name]
}

// resolveRef classifies expr as a reference to a declared resource. ok is false
// when expr is something else (a literal, a local, a function call, ...). The
// forms recognised are:
//
//	type.name                 dependency-only (all instances, for for_each)
//	type.name.attr            singular resource attribute
//	type.name["k"]            dependency-only, one instance
//	type.name["k"].attr       instance attribute
//	type.name[expr]           dependency-only, key evaluated in ctx
//	type.name[expr].attr      instance attribute, key evaluated in ctx
func (l *loader) resolveRef(expr hclsyntax.Expression, ctx *hcl.EvalContext) (refTarget, bool, error) {
	switch e := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		decl := l.declared(e.Traversal)
		if decl == nil {
			return refTarget{}, false, nil
		}
		tr := e.Traversal
		t := refTarget{typ: tr.RootName(), name: tr[1].(hcl.TraverseAttr).Name}
		rest := tr[2:]
		if len(rest) > 0 {
			if idx, isIdx := rest[0].(hcl.TraverseIndex); isIdx {
				if idx.Key.Type() != cty.String {
					return refTarget{}, false, fmt.Errorf("%s.%s: instance key must be a string", t.typ, t.name)
				}
				return l.indexed(t, decl, idx.Key.AsString(), rest[1:])
			}
		}
		attr, _ := traversalIndexName(tr, 2)
		if decl.keyed {
			if len(rest) == 0 {
				t.all = true
				return t, true, nil
			}
			return refTarget{}, false, fmt.Errorf(`%s.%s has for_each; reference a specific instance, e.g. %s.%s["<key>"].%s`, t.typ, t.name, t.typ, t.name, attr)
		}
		t.attr = attr
		return t, true, nil
	case *hclsyntax.IndexExpr:
		st, ok := e.Collection.(*hclsyntax.ScopeTraversalExpr)
		if !ok || len(st.Traversal) != 2 {
			return refTarget{}, false, nil
		}
		decl := l.declared(st.Traversal)
		if decl == nil {
			return refTarget{}, false, nil
		}
		t := refTarget{typ: st.Traversal.RootName(), name: st.Traversal[1].(hcl.TraverseAttr).Name}
		key, err := l.evalKey(e.Key, ctx, t.typ+"."+t.name)
		if err != nil {
			return refTarget{}, false, err
		}
		return l.indexed(t, decl, key, nil)
	case *hclsyntax.RelativeTraversalExpr:
		ix, ok := e.Source.(*hclsyntax.IndexExpr)
		if !ok {
			return refTarget{}, false, nil
		}
		t, ok, err := l.resolveRef(ix, ctx)
		if err != nil || !ok {
			return t, ok, err
		}
		if a, isAttr := e.Traversal[0].(hcl.TraverseAttr); isAttr {
			t.attr = a.Name
		}
		return t, true, nil
	}
	return refTarget{}, false, nil
}

// indexed completes a reference to one instance of t, checking that t has
// for_each and that the key is declared. rest is any traversal after the index.
func (l *loader) indexed(t refTarget, decl *declaredResource, key string, rest hcl.Traversal) (refTarget, bool, error) {
	base := t.typ + "." + t.name
	if !decl.keyed {
		return refTarget{}, false, fmt.Errorf("%s does not have for_each, so it can't be indexed; reference it as %s.<attr>", base, base)
	}
	if !decl.keySet[key] {
		return refTarget{}, false, fmt.Errorf("no instance %s of %s (declared: %s)", addr.Quote(key), base, keyPreview(decl.keys))
	}
	t.key = key
	if len(rest) > 0 {
		if a, ok := rest[0].(hcl.TraverseAttr); ok {
			t.attr = a.Name
		}
	}
	return t, true, nil
}

// evalKey evaluates a dynamic index expression; the result must be a known,
// non-empty string. A key that can't be evaluated here is a *dynamicKeyError.
func (l *loader) evalKey(expr hcl.Expression, ctx *hcl.EvalContext, what string) (string, error) {
	v, d := expr.Value(ctx)
	if d.HasErrors() {
		return "", &dynamicKeyError{msg: fmt.Sprintf("%s: evaluating instance key: %s", what, d.Error())}
	}
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return "", fmt.Errorf("%s: instance key must be a known string", what)
	}
	if v.AsString() == "" {
		return "", fmt.Errorf("%s: instance key must not be empty", what)
	}
	return v.AsString(), nil
}

// keyPreview lists the first few declared keys for an error message.
func keyPreview(keys []string) string {
	if len(keys) == 0 {
		return "none"
	}
	const max = 5
	shown := keys
	if len(shown) > max {
		shown = shown[:max]
	}
	s := strings.Join(shown, ", ")
	if len(keys) > max {
		s += ", …"
	}
	return s
}
