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
}

type Plan struct {
	Changes []Change
	// EmptyForEach names for_each resources (type.name) whose for_each declared
	// zero instances. Set by the caller from config; Warnings uses it to call
	// out that every instance of such a resource still in state is destroyed.
	EmptyForEach []string
}

func (p *Plan) HasChanges() bool {
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

	pl := &Plan{}
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
		st, inState := s.Find(addr)
		if !inState {
			pl.Changes = append(pl.Changes, Change{
				Address: addr, Type: d.Type, Name: d.Name, Key: d.Key, Action: provider.ActionCreate, Desired: d.Attributes, DependsOn: d.DependsOn, Refs: d.Refs,
				Sensitive: sensitiveAttrs(resources[d.Type]),
			})
			continue
		}
		actual, err := resources[d.Type].Read(ctx, st.ID)
		if errors.Is(err, provider.ErrNotFound) {
			pl.Changes = append(pl.Changes, Change{
				Address: addr, Type: d.Type, Name: d.Name, Key: d.Key, Action: provider.ActionCreate, Desired: d.Attributes, DependsOn: d.DependsOn, Refs: d.Refs,
				Sensitive: sensitiveAttrs(resources[d.Type]),
			})
			continue
		}
		if err != nil {
			return nil, err
		}
		// Resolve references from existing state so a referenced attribute
		// (e.g. an id) diffs against its real resolved value, not null — this
		// keeps re-plans convergent after apply.
		effectiveDesired := mergeRefsFromState(d, s)
		diffs := diffAttrs(actual, effectiveDesired, resources[d.Type].Computed())
		td, err := triggerDiffs(resources[d.Type], effectiveDesired, st)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, td...)
		action := provider.ActionNoop
		if len(diffs) > 0 {
			action = provider.ActionUpdate
		}
		pl.Changes = append(pl.Changes, Change{
			Address: addr, Type: d.Type, Name: d.Name, Key: d.Key, ID: st.ID, Action: action,
			Desired: d.Attributes, Actual: actual, AttrDiffs: diffs, DependsOn: d.DependsOn, Refs: d.Refs,
			Sensitive: sensitiveAttrs(resources[d.Type]),
		})
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
			return fmt.Errorf("%s is declared both with and without for_each; a resource is either singular or keyed (converting between them in place needs `moved` blocks, which are not supported yet)", base)
		}
		keyed[base] = d.Key != ""
	}
	return nil
}

// mergeRefsFromState returns d.Attributes with referenced attributes resolved
// from already-applied state, so the diff compares against real resolved values
// (keeping re-plans convergent). Unresolvable refs are left absent.
func mergeRefsFromState(d DesiredResource, s *state.State) provider.Attributes {
	out := make(provider.Attributes, len(d.Attributes)+len(d.Refs))
	for k, v := range d.Attributes {
		out[k] = v
	}
	for attr, ref := range d.Refs {
		target, ok := s.Find(ref.Address)
		if !ok {
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
		if !o.RawEquals(n) {
			diffs = append(diffs, AttrDiff{Name: k, Old: o, New: n})
		}
	}
	return diffs
}

// triggerDiffs compares a Triggerable resource's config-computed triggers against
// the triggers recorded in state. Each changed key becomes an AttrDiff that forces
// an Update, even when no declared attribute differs (e.g. a place whose file
// content changed). Resources that are not Triggerable yield no diffs.
func triggerDiffs(res provider.Resource, desired provider.Attributes, st *state.Resource) ([]AttrDiff, error) {
	t, ok := res.(provider.Triggerable)
	if !ok {
		return nil, nil
	}
	want, err := t.Triggers(desired)
	if err != nil {
		return nil, err
	}
	have := decodeAttrs(st.Triggers)
	names := make([]string, 0, len(want))
	for k := range want {
		names = append(names, k)
	}
	sort.Strings(names)
	var diffs []AttrDiff
	for _, k := range names {
		o := valOr(have, k)
		if !o.RawEquals(want[k]) {
			diffs = append(diffs, AttrDiff{Name: k, Old: o, New: want[k]})
		}
	}
	return diffs, nil
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
