package plan

import (
	"errors"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// planInState plans a desired resource that is in state and still exists
// remotely (actual is its Read): update, replace or no-op. pending marks the
// addresses that get a new remote object in this plan (their references are
// unknown until apply); importIDs gives the id of import targets that are
// adopted as-is (see ResolveImports).
func planInState(res provider.Resource, d DesiredResource, st *state.Resource, actual provider.Attributes, s *state.State, pending map[string]bool, importIDs map[string]string) (Change, error) {
	address := d.Address()
	// Resolve references from existing state so a referenced attribute (e.g. an
	// id) diffs against its real resolved value, not null — this keeps re-plans
	// convergent after apply.
	resolved, err := normalize(res, address, mergeRefs(d, s, importIDs))
	if err != nil {
		return Change{}, err
	}
	// A reference to a resource this plan creates or replaces resolves to a
	// value that only exists after apply (e.g. a re-uploaded asset's new id),
	// so diff against an unknown: the dependent is updated in the same apply,
	// once the new value is in the outputs map.
	effective := withPendingRefs(resolved, d.Refs, pending)
	forcing := replaceKeys(res, effective, actual)
	diffs := diffAttrs(actual, effective, res.Computed())
	diffs = append(diffs, pendingRefDiffs(d.Refs, pending, resolved, diffs)...)
	td, backfill, warning, err := triggerDiffs(res, address, resolved, st, forcing)
	if err != nil {
		return Change{}, err
	}
	diffs = append(diffs, td...)
	action, replace := classify(diffs, forcing)
	return Change{
		Address: address, Type: d.Type, Name: d.Name, Key: d.Key, ID: st.ID, Action: action,
		Desired: d.Attributes, Actual: actual, AttrDiffs: diffs, DependsOn: d.DependsOn, Refs: d.Refs,
		Sensitive: sensitiveAttrs(res), Replace: replace,
		AbandonsOld:      action == provider.ActionReplace && abandons(res),
		BackfillTriggers: backfill, Warning: warning,
	}, nil
}

// withPendingRefs returns desired with every referenced attribute whose target
// gets a new remote object in this plan (pending) replaced by an unknown value.
// desired is not mutated.
func withPendingRefs(desired provider.Attributes, refs map[string]provider.Ref, pending map[string]bool) provider.Attributes {
	var hit bool
	for _, ref := range refs {
		if pending[ref.Address] {
			hit = true
			break
		}
	}
	if !hit {
		return desired
	}
	out := make(provider.Attributes, len(desired)+len(refs))
	for k, v := range desired {
		out[k] = v
	}
	for attr, ref := range refs {
		if pending[ref.Address] {
			out[attr] = cty.UnknownVal(cty.DynamicPseudoType)
		}
	}
	return out
}

// pendingRefDiffs returns a diff for every referenced attribute whose target is
// pending but that diffAttrs didn't report, because it is Computed (e.g. a
// create-only input). The dependent must still be planned, at least as an
// update, so the new value reaches state; when the attribute forces
// replacement it is replaced instead.
func pendingRefDiffs(refs map[string]provider.Ref, pending map[string]bool, resolved provider.Attributes, have []AttrDiff) []AttrDiff {
	seen := map[string]bool{}
	for _, d := range have {
		seen[d.Name] = true
	}
	var names []string
	for attr, ref := range refs {
		if pending[ref.Address] && !seen[attr] {
			names = append(names, attr)
		}
	}
	sort.Strings(names)
	var out []AttrDiff
	for _, n := range names {
		out = append(out, AttrDiff{Name: n, Old: valOr(resolved, n), New: cty.UnknownVal(cty.DynamicPseudoType)})
	}
	return out
}

// replaceKeys returns the set of attribute/trigger names whose change forces a
// replace of this resource (provider.Replacer), or nil.
func replaceKeys(res provider.Resource, desired, actual provider.Attributes) map[string]bool {
	r, ok := res.(provider.Replacer)
	if !ok {
		return nil
	}
	keys := r.RequiresReplace(desired, actual)
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

func abandons(res provider.Resource) bool {
	r, ok := res.(provider.Replacer)
	return ok && r.ReplaceAbandons()
}

// classify picks the action for an in-state resource from its diffs: noop when
// nothing differs, replace when any differing name forces replacement (those
// names are returned, sorted), update otherwise.
func classify(diffs []AttrDiff, forcing map[string]bool) (provider.ChangeAction, []string) {
	if len(diffs) == 0 {
		return provider.ActionNoop, nil
	}
	var replace []string
	for _, d := range diffs {
		if forcing[d.Name] {
			replace = append(replace, d.Name)
		}
	}
	if len(replace) > 0 {
		sort.Strings(replace)
		return provider.ActionReplace, replace
	}
	return provider.ActionUpdate, nil
}

// triggerDiffs compares a Triggerable resource's config-computed triggers against
// the triggers recorded in state. Each changed key becomes an AttrDiff that forces
// an update (or a replace, for forcing keys), even when no declared attribute
// differs (e.g. a place whose file content changed). Resources that are not
// Triggerable yield nothing.
//
// A key with no value recorded in state is not diffed: the state predates the
// trigger (or the input was missing when it was recorded), so there is nothing
// to compare with, and re-uploading or replacing every such object (e.g. each
// asset uploaded before roblox_asset had a content_hash) would be wasteful or
// destructive. Such keys are returned as backfill, for apply to record. Every
// Triggerable resource records its triggers from its first create or import,
// so this only affects state written before a trigger existed.
//
// When an input is missing (provider.TriggerUnavailableError, e.g. a source
// file absent on this machine), the triggers that couldn't be computed keep
// their recorded values (no change detected) and a warning is returned.
func triggerDiffs(res provider.Resource, address string, desired provider.Attributes, st *state.Resource, forcing map[string]bool) (diffs []AttrDiff, backfill []string, warning string, err error) {
	t, ok := res.(provider.Triggerable)
	if !ok {
		return nil, nil, "", nil
	}
	want, err := t.Triggers(desired)
	var unavailable *provider.TriggerUnavailableError
	if errors.As(err, &unavailable) {
		warning = fmt.Sprintf("%s: %s; content changes can't be detected until it is available.", address, unavailable.Reason)
		err = nil
	}
	if err != nil {
		return nil, nil, "", err
	}
	have := decodeAttrs(st.Triggers)
	names := make([]string, 0, len(want))
	for k := range want {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		o, recorded := have[k]
		if !recorded {
			backfill = append(backfill, k)
			continue
		}
		if !o.RawEquals(want[k]) {
			diffs = append(diffs, AttrDiff{Name: k, Old: o, New: want[k]})
		}
	}
	return diffs, backfill, warning, nil
}

// replanDependents re-plans, in dependency order, every in-state change that
// references an address whose "gets a new id" status changed after Build
// (flipped), e.g. an import target: Build saw it as a create (pending), but an
// import that adopts it as-is keeps the imported id, and an import that is then
// replaced does get a new id. A re-planned change whose own status flips
// cascades to its dependents.
func (p *Plan) replanDependents(resources map[string]provider.Resource, s *state.State, flipped map[string]bool, importIDs map[string]string) error {
	if len(flipped) == 0 || p.desired == nil {
		return nil
	}
	for i := range p.Changes {
		c := &p.Changes[i]
		if c.ImportID != "" || c.ID == "" || c.Action == provider.ActionDelete || c.Action == provider.ActionCreate {
			continue
		}
		touched := false
		for _, ref := range c.Refs {
			if flipped[ref.Address] {
				touched = true
				break
			}
		}
		if !touched {
			continue
		}
		st, ok := s.Find(c.Address)
		d, declared := p.desired[c.Address]
		if !ok || !declared {
			continue
		}
		wasReplace := c.Action == provider.ActionReplace
		nc, err := planInState(resources[c.Type], d, st, c.Actual, s, p.pending, importIDs)
		if err != nil {
			return err
		}
		*c = nc
		if isReplace := c.Action == provider.ActionReplace; isReplace != wasReplace {
			p.pending[c.Address] = isReplace
			flipped[c.Address] = true
		}
	}
	return nil
}
