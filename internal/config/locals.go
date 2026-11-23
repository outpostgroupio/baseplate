package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// evalLocals evaluates every `locals {}` block. Locals may reference other
// locals (evaluated in dependency order; cycles are an error), `path.config`,
// env() and the function library, but not resources, so every local is known
// at load time (which for_each needs). Names must be unique across blocks.
func (l *loader) evalLocals(blocks hcl.Blocks) (map[string]cty.Value, error) {
	attrs := map[string]*hcl.Attribute{}
	for _, b := range blocks {
		if b.Type != "locals" {
			continue
		}
		as, d := b.Body.JustAttributes()
		if d.HasErrors() {
			return nil, d
		}
		for name, a := range as {
			if prev, dup := attrs[name]; dup {
				return nil, fmt.Errorf("%s: duplicate local %q (also defined at %s)", a.NameRange, name, prev.NameRange)
			}
			attrs[name] = a
		}
	}

	deps := map[string][]string{}
	for name, a := range attrs {
		for _, tr := range a.Expr.Variables() {
			root := tr.RootName()
			switch {
			case root == "each":
				return nil, noEach([]hcl.Traversal{tr})
			case root == "local":
				dep, ok := traversalIndexName(tr, 1)
				if !ok {
					return nil, fmt.Errorf("%s: invalid reference to local values; use local.<name>", tr.SourceRange())
				}
				if _, ok := attrs[dep]; !ok {
					return nil, fmt.Errorf("%s: local.%s references an undeclared local %q", tr.SourceRange(), name, dep)
				}
				deps[name] = append(deps[name], dep)
			case l.resources[root] != nil:
				return nil, fmt.Errorf("%s: local.%s references %s; locals can't reference resources (their values are only known at apply time)", tr.SourceRange(), name, traversalString(tr))
			}
		}
	}
	order, err := localOrder(attrs, deps)
	if err != nil {
		return nil, err
	}

	values := map[string]cty.Value{}
	funcs := functions()
	pathVal := pathObject(l.path)
	for _, name := range order {
		ctx := &hcl.EvalContext{
			Variables: map[string]cty.Value{"local": cty.ObjectVal(values), "path": pathVal},
			Functions: funcs,
		}
		v, d := attrs[name].Expr.Value(ctx)
		if d.HasErrors() {
			return nil, d
		}
		values[name] = v
	}
	return values, nil
}

// localOrder returns local names so each comes after the locals it references
// (ties broken lexically), or an error naming a dependency cycle.
func localOrder(attrs map[string]*hcl.Attribute, deps map[string][]string) ([]string, error) {
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)

	const (
		unvisited = iota
		visiting
		done
	)
	mark := map[string]int{}
	var order, stack []string
	var visit func(string) error
	visit = func(n string) error {
		switch mark[n] {
		case done:
			return nil
		case visiting:
			i := 0
			for stack[i] != n {
				i++
			}
			cycle := append(append([]string{}, stack[i:]...), n)
			return fmt.Errorf("%s: locals have a dependency cycle: local.%s", attrs[n].NameRange, strings.Join(cycle, " -> local."))
		}
		mark[n] = visiting
		stack = append(stack, n)
		ds := append([]string{}, deps[n]...)
		sort.Strings(ds)
		for _, d := range ds {
			if err := visit(d); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		mark[n] = done
		order = append(order, n)
		return nil
	}
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return order, nil
}
