package plan

import (
	"context"
	"errors"
	"fmt"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// ImportBlock is a declarative `import {}` block: adopt the resource at Address
// (which must have a resource block) using the remote id ID.
type ImportBlock struct {
	Address string
	ID      string
}

// ResolveImports rewrites the plan in place for declarative import blocks. A
// pending import (target not yet in state) becomes an import Change that adopts
// the live resource and reconciles it to config. An already-in-state target is
// left unchanged (the import is already satisfied). Triggers are intentionally
// not diffed here — the adopt step (apply) sets the trigger baseline from config,
// so a triggerable resource imports cleanly.
func ResolveImports(ctx context.Context, p provider.Provider, s *state.State, pl *Plan, imports []ImportBlock) error {
	resources := p.Resources()
	idx := map[string]int{}
	for i := range pl.Changes {
		idx[pl.Changes[i].Address] = i
	}
	seen := map[string]bool{}
	for _, imp := range imports {
		if seen[imp.Address] {
			return fmt.Errorf("duplicate import block for %q", imp.Address)
		}
		seen[imp.Address] = true
		i, ok := idx[imp.Address]
		if !ok {
			return fmt.Errorf("import target %q is not declared as a resource (add a resource block, or run plan --generate-config=FILE to generate one)", imp.Address)
		}
		c := &pl.Changes[i]
		if c.Action != provider.ActionCreate {
			continue // already in state → import already satisfied
		}
		r := resources[c.Type]
		actual, err := r.Read(ctx, imp.ID)
		if err != nil {
			if errors.Is(err, provider.ErrNotFound) {
				return fmt.Errorf("import %s: no %s found with id %q", imp.Address, c.Type, imp.ID)
			}
			return err
		}
		d := DesiredResource{Type: c.Type, Name: nameFromAddress(c.Address), Attributes: c.Desired, Refs: c.Refs}
		effectiveDesired, err := normalize(r, c.Address, mergeRefsFromState(d, s))
		if err != nil {
			return err
		}
		diffs := diffAttrs(actual, effectiveDesired, r.Computed())
		c.ImportID = imp.ID
		c.ID = imp.ID
		c.Actual = actual
		c.AttrDiffs = diffs
		if len(diffs) > 0 {
			c.Action = provider.ActionUpdate
		} else {
			c.Action = provider.ActionNoop
		}
	}
	return nil
}

func nameFromAddress(address string) string {
	for i := 0; i < len(address); i++ {
		if address[i] == '.' {
			return address[i+1:]
		}
	}
	return address
}
