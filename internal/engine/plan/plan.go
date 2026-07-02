package plan

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroup/baseplate/internal/engine/graph"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/state"
)

type DesiredResource struct {
	Type       string
	Name       string
	Attributes provider.Attributes
	DependsOn  []string
}

func (d DesiredResource) Address() string { return d.Type + "." + d.Name }

type AttrDiff struct {
	Name string
	Old  cty.Value
	New  cty.Value
}

type Change struct {
	Address   string
	Type      string
	ID        string
	Action    provider.ChangeAction
	Desired   provider.Attributes
	Actual    provider.Attributes
	AttrDiffs []AttrDiff
}

type Plan struct {
	Changes []Change
}

func (p *Plan) HasChanges() bool {
	for _, c := range p.Changes {
		if c.Action != provider.ActionNoop {
			return true
		}
	}
	return false
}

func Build(ctx context.Context, p provider.Provider, s *state.State, desired []DesiredResource) (*Plan, error) {
	resources := p.Resources()
	byAddr := map[string]DesiredResource{}
	var nodes []graph.Node
	for _, d := range desired {
		byAddr[d.Address()] = d
		nodes = append(nodes, graph.Node{Address: d.Address(), DependsOn: d.DependsOn})
	}
	// deletions: in state, not desired.
	deletes := map[string]state.Resource{}
	for _, r := range s.Resources {
		if _, ok := byAddr[r.Address()]; !ok {
			deletes[r.Address()] = r
			nodes = append(nodes, graph.Node{Address: r.Address()})
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
				Address: addr, Type: r.Type, ID: r.ID, Action: provider.ActionDelete,
				Actual: decodeAttrs(r.Attributes),
			})
			continue
		}
		d := byAddr[addr]
		st, inState := s.Find(addr)
		if !inState {
			pl.Changes = append(pl.Changes, Change{
				Address: addr, Type: d.Type, Action: provider.ActionCreate, Desired: d.Attributes,
			})
			continue
		}
		actual, err := resources[d.Type].Read(ctx, st.ID)
		if errors.Is(err, provider.ErrNotFound) {
			pl.Changes = append(pl.Changes, Change{
				Address: addr, Type: d.Type, Action: provider.ActionCreate, Desired: d.Attributes,
			})
			continue
		}
		if err != nil {
			return nil, err
		}
		diffs := diffAttrs(actual, d.Attributes)
		action := provider.ActionNoop
		if len(diffs) > 0 {
			action = provider.ActionUpdate
		}
		pl.Changes = append(pl.Changes, Change{
			Address: addr, Type: d.Type, ID: st.ID, Action: action,
			Desired: d.Attributes, Actual: actual, AttrDiffs: diffs,
		})
	}
	// deletes must run in reverse dependency order; re-sort delete block.
	reorderDeletes(pl)
	return pl, nil
}

func diffAttrs(actual, desired provider.Attributes) []AttrDiff {
	keys := map[string]bool{}
	for k := range actual {
		keys[k] = true
	}
	for k := range desired {
		keys[k] = true
	}
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)

	var diffs []AttrDiff
	for _, k := range names {
		o := valOr(actual, k)
		n := valOr(desired, k)
		if !o.RawEquals(n) {
			diffs = append(diffs, AttrDiff{Name: k, Old: o, New: n})
		}
	}
	return diffs
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
