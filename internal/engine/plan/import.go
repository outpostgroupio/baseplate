package plan

import (
	"context"
	"errors"
	"fmt"

	"github.com/zclconf/go-cty/cty"

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
// left unchanged (the import is already satisfied). An adopted object whose
// declared attributes differ in a replace-forcing attribute (e.g. a different
// asset_type) is imported and then replaced, as in Terraform. Triggers are
// intentionally not diffed here: the adopt step (apply) sets the trigger
// baseline from config, so a triggerable resource imports cleanly and an
// import never replaces on content. The exception is a trigger the platform
// can't report (provider.UnreadableTriggers, e.g. a place's content): adopting
// the config's value would claim the live object matches it, so the import is
// followed by an update.
//
// Build planned each import target as a create, so their dependents were
// planned against an unknown id. Once each import is resolved, dependents are
// re-planned: against the imported id when the object is adopted as-is, or
// still unknown when it is then replaced.
func ResolveImports(ctx context.Context, p provider.Provider, s *state.State, pl *Plan, imports []ImportBlock) error {
	resources := p.Resources()
	idx := map[string]int{}
	for i := range pl.Changes {
		idx[pl.Changes[i].Address] = i
	}
	seen := map[string]bool{}
	claimed := map[[2]string]string{}     // {type, id} -> address importing it
	pendingImports := map[string]string{} // address -> import id, for targets not yet in state
	actuals := map[string]provider.Attributes{}
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
		pendingImports[imp.Address] = imp.ID
		if _, fromConfig := resources[c.Type].(provider.StateImporter); fromConfig {
			continue // taken from the resolved config below
		}
		actual, err := resources[c.Type].Read(ctx, imp.ID)
		if err != nil {
			if errors.Is(err, provider.ErrNotFound) {
				return fmt.Errorf("import %s: no %s found with id %q", imp.Address, c.Type, imp.ID)
			}
			return provider.ResourceError(imp.Address, c.Type, resources[c.Type], err)
		}
		actuals[imp.Address] = actual
	}
	if len(pendingImports) == 0 {
		return nil
	}
	pending := pl.pending
	if pending == nil {
		pending = map[string]bool{}
		pl.pending = pending
	}
	// Resolve each import in dependency order (Changes are topologically
	// sorted), so an import that references another import target sees its
	// final status, then re-plan the in-state dependents of any target whose
	// "gets a new id" status changed.
	importIDs := map[string]string{}
	flipped := map[string]bool{}
	for i := range pl.Changes {
		c := &pl.Changes[i]
		id, ok := pendingImports[c.Address]
		if !ok {
			continue
		}
		r := resources[c.Type]
		actual := actuals[c.Address]
		d := DesiredResource{Type: c.Type, Name: c.Name, Key: c.Key, Attributes: c.Desired, Refs: c.Refs, PathRefs: c.PathRefs}
		resolved, err := normalize(r, c.Address, mergeRefs(d, s, importIDs))
		if err != nil {
			return err
		}
		effective, err := withPendingRefs(resolved, c.Refs, c.PathRefs, pending)
		if err != nil {
			return err
		}
		if si, ok := r.(provider.StateImporter); ok {
			if actual, err = si.ImportState(id, effective); err != nil {
				return fmt.Errorf("import %s: %w", c.Address, err)
			}
		}
		diffs, censored := dropCensored(r, diffAttrs(actual, effective, r.Computed()))
		ud, err := unverifiedDiffs(r, resolved)
		if err != nil {
			return err
		}
		diffs = append(diffs, ud...)
		c.ImportID = id
		c.ID = id
		c.Actual = actual
		c.AttrDiffs = diffs
		c.Censored = censored
		c.Action, c.Replace = classify(diffs, replaceKeys(r, effective, actual))
		c.AbandonsOld = c.Action == provider.ActionReplace && abandons(r)
		replaced := c.Action == provider.ActionReplace
		if !replaced {
			importIDs[c.Address] = id
		}
		if pending[c.Address] != replaced {
			pending[c.Address] = replaced
			flipped[c.Address] = true
		}
	}
	return pl.replanDependents(resources, s, flipped, importIDs)
}

// unverifiedDiffs returns, for an import, a diff from provider.UnverifiedTrigger
// to the config's value for each trigger the platform can't report back. A
// trigger whose input is missing (provider.TriggerUnavailableError) is skipped;
// apply records it as unverified anyway, so a later plan catches it up.
func unverifiedDiffs(r provider.Resource, desired provider.Attributes) ([]AttrDiff, error) {
	u, ok := r.(provider.UnreadableTriggers)
	t, isTriggerable := r.(provider.Triggerable)
	if !ok || !isTriggerable {
		return nil, nil
	}
	want, err := t.Triggers(desired)
	var unavailable *provider.TriggerUnavailableError
	if err != nil && !errors.As(err, &unavailable) {
		return nil, err
	}
	var diffs []AttrDiff
	for _, k := range u.UnreadableTriggers() {
		if v, ok := want[k]; ok {
			diffs = append(diffs, AttrDiff{Name: k, Old: cty.StringVal(provider.UnverifiedTrigger), New: v})
		}
	}
	return diffs, nil
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
