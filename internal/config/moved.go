package config

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

// expandMoved turns one moved block into one plan.MovedBlock per for_each
// instance (or exactly one without for_each). `from` and `to` are addresses,
// not values: type.name, type.name["key"], or type.name[expr] where expr may
// use each.key / each.value. for_each on a moved block is a Baseplate
// extension (Terraform has none); it lets a whole directory of for_each
// instances move in one block.
func (l *loader) expandMoved(b *hcl.Block, ctx *hcl.EvalContext, resTypes map[string]provider.Resource) ([]plan.MovedBlock, error) {
	attrs, d := b.Body.JustAttributes()
	if d.HasErrors() {
		return nil, d
	}
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if n != "from" && n != "to" && n != "for_each" {
			return nil, fmt.Errorf("%s: moved block: unsupported argument %q (expected from, to and optionally for_each)", attrs[n].NameRange, n)
		}
	}
	fromAttr, ok := attrs["from"]
	if !ok {
		return nil, fmt.Errorf("%s: moved block requires a `from` argument", b.DefRange)
	}
	toAttr, ok := attrs["to"]
	if !ok {
		return nil, fmt.Errorf("%s: moved block requires a `to` argument", b.DefRange)
	}
	keys := []string{""}
	var values map[string]cty.Value
	fe, hasForEach := attrs["for_each"]
	if hasForEach {
		var err error
		if keys, values, err = l.evalForEach(fe, ctx, "moved"); err != nil {
			return nil, err
		}
	} else if err := noEach(append(fromAttr.Expr.Variables(), toAttr.Expr.Variables()...)); err != nil {
		return nil, err
	}
	var out []plan.MovedBlock
	for _, key := range keys {
		ictx := ctx
		if hasForEach {
			ictx = eachContext(ctx, key, values[key])
		}
		ft, fn, fk, fKeyed, err := l.addressExpr(fromAttr.Expr, ictx, "moved `from`")
		if err != nil {
			return nil, err
		}
		tt, tn, tk, tKeyed, err := l.addressExpr(toAttr.Expr, ictx, "moved `to`")
		if err != nil {
			return nil, err
		}
		from, to := addr.Format(ft, fn, fk), addr.Format(tt, tn, tk)
		if _, ok := resTypes[ft]; !ok {
			return nil, fmt.Errorf("%s: moved block from %s: unknown resource type %q", l.path, from, ft)
		}
		if ft != tt {
			return nil, fmt.Errorf("%s: moved block from %s to %s: the resource type differs (%s vs %s); a move can only rename a resource, not change its type", l.path, from, to, ft, tt)
		}
		if from == to {
			return nil, fmt.Errorf("%s: moved block from %s to %s: from and to are the same address", l.path, from, to)
		}
		if err := l.fromStillDeclared(ft, fn, fk, fKeyed, tKeyed, from, to); err != nil {
			return nil, err
		}
		out = append(out, plan.MovedBlock{From: from, To: to})
	}
	return out, nil
}

// fromStillDeclared rejects a move whose `from` is still declared in config,
// as Terraform does: the old address must be gone, or plan would both keep
// the old object and move it away.
func (l *loader) fromStillDeclared(typ, name, key string, fromKeyed, toKeyed bool, from, to string) error {
	decl := l.resources[typ][name]
	if decl == nil {
		return nil
	}
	whole := !fromKeyed && !toKeyed
	var still bool
	switch {
	case whole:
		still = true
	case fromKeyed:
		still = decl.keyed && decl.keySet[key]
	default:
		still = !decl.keyed
	}
	if !still {
		return nil
	}
	what := "resource"
	if !whole {
		what = "instance"
	}
	return fmt.Errorf("%s: moved block from %s to %s: %s is still declared in the configuration. A moved block's `from` is the old address: change the configuration so that this %s is declared as %s instead", l.path, from, to, from, what, to)
}

// checkMovedSet rejects two moves out of (or into) the same address: which
// one wins would depend on state, not on the config.
func (l *loader) checkMovedSet(moved []plan.MovedBlock) error {
	froms, tos := map[string]string{}, map[string]string{}
	for _, m := range moved {
		if other, dup := froms[m.From]; dup {
			return fmt.Errorf("%s: ambiguous moved blocks: %s is moved to both %s and %s", l.path, m.From, other, m.To)
		}
		froms[m.From] = m.To
		if other, dup := tos[m.To]; dup {
			return fmt.Errorf("%s: ambiguous moved blocks: both %s and %s are moved to %s", l.path, other, m.From, m.To)
		}
		tos[m.To] = m.From
	}
	return nil
}
