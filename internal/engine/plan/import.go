package plan

import (
	"context"
	"errors"
	"fmt"

	"github.com/outpostgroupio/baseplate/internal/addr"
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
	claimed := map[[2]string]string{} // {type, id} -> address importing it
	for _, imp := range imports {
		if seen[imp.Address] {
			return fmt.Errorf("duplicate import block for %q", imp.Address)
		}
		seen[imp.Address] = true
		i, ok := idx[imp.Address]
		if !ok {
			_, _, _, hasKey, err := addr.Parse(imp.Address)
			if err != nil {
				return fmt.Errorf("import target: %w", err)
			}
			if hasKey {
				return fmt.Errorf("import target %q is not a declared instance (no resource block's for_each produces that key)", imp.Address)
			}
			return fmt.Errorf("import target %q is not declared as a resource (add a resource block, or run plan --generate-config=FILE to generate one)", imp.Address)
		}
		c := &pl.Changes[i]
		if prev, dup := claimed[[2]string{c.Type, imp.ID}]; dup {
			return fmt.Errorf("import id %q is used for both %s and %s; one remote object can only be managed at one address", imp.ID, prev, imp.Address)
		}
		claimed[[2]string{c.Type, imp.ID}] = imp.Address
		if err := ImportCollision(s, c.Type, imp.Address, imp.ID); err != nil {
			return err
		}
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
		d := DesiredResource{Type: c.Type, Name: c.Name, Key: c.Key, Attributes: c.Desired, Refs: c.Refs}
		effectiveDesired := mergeRefsFromState(d, s)
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

// ImportCollision returns an error when a state entry other than address
// already manages the remote object (typ, id). Two addresses managing one
// object is never safe: when one of them is later destroyed, the object the
// other still manages is deleted with it (a hard delete for some types).
func ImportCollision(s *state.State, typ, address, id string) error {
	if other, ok := s.FindByID(typ, id); ok && other.Address() != address {
		return fmt.Errorf("import %s: %s id %q is already managed as %s; one remote object can only be managed at one address", address, typ, id, other.Address())
	}
	return nil
}
