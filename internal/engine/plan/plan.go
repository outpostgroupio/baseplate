package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/engine/graph"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
)

type DesiredResource struct {
	Type string
	Name string
	// Key is the for_each instance key; empty for a resource without for_each.
	Key        string
	Attributes provider.Attributes
	DependsOn  []string
	// Refs maps an attribute name to the resource output it references
	// (e.g. "icon" -> roblox_asset.gem_icon.id). Referenced attributes are
	// resolved at apply time and are NOT present in Attributes.
	Refs map[string]provider.Ref
	// PreventDestroy is the resource block's lifecycle.prevent_destroy. It is
	// known only from config (never persisted to state), so removing the block
	// or its lifecycle setting removes the protection, as in Terraform.
	PreventDestroy bool
}

func (d DesiredResource) Address() string { return addr.Format(d.Type, d.Name, d.Key) }

type AttrDiff struct {
	Name string
	Old  cty.Value
	New  cty.Value
}

type Change struct {
	Address   string
	Type      string
	Name      string
	Key       string // for_each instance key; empty for a singular resource
	ID        string
	Action    provider.ChangeAction
	ImportID  string
	Desired   provider.Attributes
	Actual    provider.Attributes
	AttrDiffs []AttrDiff
	DependsOn []string
	Refs      map[string]provider.Ref
	Sensitive []string
	// Replace names the attributes and/or triggers whose change forces this
	// replace (Action == ActionReplace); see provider.Replacer.
	Replace []string
	// AbandonsOld is set on a replace whose old object is left in place (only
	// dropped from state) rather than deleted; see provider.Replacer.
	AbandonsOld bool
	// BackfillTriggers names triggers with no value recorded in state that
	// plan adopted as a baseline instead of diffing; apply records them.
	BackfillTriggers []string
	// Warning, when set, is a plan warning about this change (e.g. a trigger
	// input that is missing, so changes to it can't be detected).
	Warning string
}

type Plan struct {
	Changes []Change
	// EmptyForEach names for_each resources (type.name) whose for_each declared
	// zero instances. Set by the caller from config; Warnings uses it to call
	// out that every instance of such a resource still in state is destroyed.
	EmptyForEach []string
	// PreventDestroy names the resource blocks (type.name) whose config sets
	// lifecycle.prevent_destroy = true. Build fills it from the desired
	// resources; callers add blocks that have no desired instance (destroy,
	// or an empty for_each) from config. Checked by CheckPreventDestroy.
	PreventDestroy []string
	// Moves lists the state entries that moved {} blocks relocated before this
	// plan was built (see ApplyMoves). They are changes in their own right:
	// apply persists them even when nothing else changes. --target doesn't
	// filter them (they're state-only and idempotent).
	Moves []Move

	// desired and pending carry Build's inputs to ResolveImports, which
	// re-plans dependents of import targets (see replanDependents).
	desired map[string]DesiredResource
	pending map[string]bool
}

func (p *Plan) HasChanges() bool {
	if len(p.Moves) > 0 {
		return true
	}
	for _, c := range p.Changes {
		if c.Action != provider.ActionNoop || c.ImportID != "" {
			return true
		}
	}
	return false
}

func Build(ctx context.Context, p provider.Provider, s *state.State, desired []DesiredResource) (*Plan, error) {
	resources := p.Resources()
	if err := checkMixedKeys(desired); err != nil {
		return nil, err
	}
	byAddr := map[string]DesiredResource{}
	var nodes []graph.Node
	for _, d := range desired {
		byAddr[d.Address()] = d
		nodes = append(nodes, graph.Node{Address: d.Address(), DependsOn: d.DependsOn})
	}
	// deletions: in state, not desired. Carry the recorded dependency edges so
	// deletes are ordered by real (reverse) dependency order, not lexical luck.
	// A recorded edge can name something that no longer exists (a dependent left
	// unchanged while one of its dependency instances was removed keeps the old
	// list), so only edges to resources still in config or state are kept.
	known := map[string]bool{}
	for _, r := range s.Resources {
		known[r.Address()] = true
	}
	deletes := map[string]state.Resource{}
	for _, r := range s.Resources {
		if _, ok := byAddr[r.Address()]; !ok {
			deletes[r.Address()] = r
			var deps []string
			for _, d := range r.Dependencies {
				if _, desired := byAddr[d]; desired || known[d] {
					deps = append(deps, d)
				}
			}
			nodes = append(nodes, graph.Node{Address: r.Address(), DependsOn: deps})
		}
	}
	order, err := graph.TopoSort(nodes)
	if err != nil {
		return nil, err
	}

	pl := &Plan{PreventDestroy: protectedBlocks(desired), desired: byAddr}
	// newID marks the addresses this plan gives a new remote object (create or
	// replace). Their outputs, the id above all, aren't known until apply, so a
	// dependent that references one is planned against an unknown value.
	newID := map[string]bool{}
	pl.pending = newID
	for _, addr := range order {
		if r, isDelete := deletes[addr]; isDelete {
			pl.Changes = append(pl.Changes, Change{
				Address: addr, Type: r.Type, Name: r.Name, Key: r.Key, ID: r.ID, Action: provider.ActionDelete,
				Actual:    decodeAttrs(r.Attributes),
				Sensitive: sensitiveAttrs(resources[r.Type]),
			})
			continue
		}
		d := byAddr[addr]
		// Validate/canonicalise up front so a bad value (e.g. a malformed
		// timestamp) fails at plan time, for creates as well as updates.
		if _, err := normalize(resources[d.Type], addr, d.Attributes); err != nil {
			return nil, err
		}
		st, inState := s.Find(addr)
		if !inState {
			newID[addr] = true
			pl.Changes = append(pl.Changes, Change{
				Address: addr, Type: d.Type, Name: d.Name, Key: d.Key, Action: provider.ActionCreate, Desired: d.Attributes, DependsOn: d.DependsOn, Refs: d.Refs,
				Sensitive: sensitiveAttrs(resources[d.Type]),
			})
			continue
		}
		actual, err := resources[d.Type].Read(ctx, st.ID)
		if errors.Is(err, provider.ErrNotFound) {
			newID[addr] = true
			pl.Changes = append(pl.Changes, Change{
				Address: addr, Type: d.Type, Name: d.Name, Key: d.Key, Action: provider.ActionCreate, Desired: d.Attributes, DependsOn: d.DependsOn, Refs: d.Refs,
				Sensitive: sensitiveAttrs(resources[d.Type]),
			})
			continue
		}
		if err != nil {
			return nil, err
		}
		c, err := planInState(resources[d.Type], d, st, actual, s, newID, nil)
		if err != nil {
			return nil, err
		}
		if c.Action == provider.ActionReplace {
			newID[addr] = true
		}
		pl.Changes = append(pl.Changes, c)
	}
	// deletes must run in reverse dependency order; re-sort delete block.
	reorderDeletes(pl)
	return pl, nil
}

// checkMixedKeys rejects a desired set in which the same type.name is both a
// singular resource and a for_each resource. Config loading can't produce this
// (one block per type.name), but the engine guards its own invariant.
func checkMixedKeys(desired []DesiredResource) error {
	keyed := map[string]bool{}
	for _, d := range desired {
		base := addr.Format(d.Type, d.Name, "")
		if was, seen := keyed[base]; seen && was != (d.Key != "") {
			return fmt.Errorf("%s is declared both with and without for_each; a resource is either singular or keyed (to convert one into the other, declare it once and add a moved block, e.g. moved { from = %s  to = %s[\"key\"] })", base, base, base)
		}
		keyed[base] = d.Key != ""
	}
	return nil
}

// mergeRefsFromState returns d.Attributes with referenced attributes resolved
// from already-applied state, so the diff compares against real resolved values
// (keeping re-plans convergent). Unresolvable refs are left absent.
func mergeRefsFromState(d DesiredResource, s *state.State) provider.Attributes {
	return mergeRefs(d, s, nil)
}

// mergeRefs is mergeRefsFromState plus importIDs: the id an import target
// will have once adopted (it isn't in state yet), so a reference to its id
// resolves to a known value.
func mergeRefs(d DesiredResource, s *state.State, importIDs map[string]string) provider.Attributes {
	out := make(provider.Attributes, len(d.Attributes)+len(d.Refs))
	for k, v := range d.Attributes {
		out[k] = v
	}
	for attr, ref := range d.Refs {
		target, ok := s.Find(ref.Address)
		if !ok {
			if id, imported := importIDs[ref.Address]; imported && ref.Attr == "id" {
				out[attr] = cty.StringVal(id)
			}
			continue
		}
		if ref.Attr == "id" {
			out[attr] = cty.StringVal(target.ID)
			continue
		}
		if raw, ok := target.Attributes[ref.Attr]; ok {
			if v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType); err == nil {
				out[attr] = v
			}
		}
	}
	return out
}

// diffAttrs compares actual vs desired over ONLY the attributes the config
// declares (desired), minus any computed attributes. Attributes the provider
// returns that the config does not declare are not managed and never diff — so
// a resource's many optional/unset fields don't produce spurious drift. Drift
// on a declared attribute is still detected (it's in desired). Computed attrs
// are excluded even if present in desired (e.g. after an import round-trip).
func diffAttrs(actual, desired provider.Attributes, computed []string) []AttrDiff {
	skip := map[string]bool{}
	for _, c := range computed {
		skip[c] = true
	}
	var names []string
	for k := range desired {
		if !skip[k] {
			names = append(names, k)
		}
	}
	sort.Strings(names)

	var diffs []AttrDiff
	for _, k := range names {
		o := valOr(actual, k)
		n := desired[k]
		if !o.RawEquals(n) && !sameJSON(o, n) {
			diffs = append(diffs, AttrDiff{Name: k, Old: o, New: n})
		}
	}
	return diffs
}

// sameJSON reports whether two wholly-known, non-null values have identical JSON
// encodings. It complements RawEquals for structural attributes, whose cty TYPE
// can legitimately differ between config and read-back while the value is the
// same — e.g. a map(string) vs an object with the same keys, or a list vs a
// tuple. JSON keeps the distinctions that matter remotely (the string "1" is not
// the number 1), so it never hides a real change.
func sameJSON(a, b cty.Value) bool {
	if a.IsNull() || b.IsNull() || !a.IsWhollyKnown() || !b.IsWhollyKnown() {
		return false
	}
	aj, err := ctyjson.Marshal(a, a.Type())
	if err != nil {
		return false
	}
	bj, err := ctyjson.Marshal(b, b.Type())
	if err != nil {
		return false
	}
	return string(aj) == string(bj)
}

func valOr(a provider.Attributes, k string) cty.Value {
	if v, ok := a[k]; ok {
		return v
	}
	return cty.NullVal(cty.DynamicPseudoType)
}

func reorderDeletes(pl *Plan) {
	var others, dels []Change
	for _, c := range pl.Changes {
		if c.Action == provider.ActionDelete {
			dels = append(dels, c)
		} else {
			others = append(others, c)
		}
	}
	for i, j := 0, len(dels)-1; i < j; i, j = i+1, j-1 {
		dels[i], dels[j] = dels[j], dels[i]
	}
	pl.Changes = append(others, dels...)
}

func decodeAttrs(m map[string]json.RawMessage) provider.Attributes {
	out := provider.Attributes{}
	for k, raw := range m {
		v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType)
		if err == nil {
			out[k] = v
		}
	}
	return out
}

func sensitiveAttrs(res provider.Resource) []string {
	if s, ok := res.(provider.Sensitive); ok {
		return s.Sensitive()
	}
	return nil
}
