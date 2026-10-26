package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// Apply executes pl against the provider, writing state after every change.
// s must be the state pl was built from, with pl.Moves already applied to it
// (plan.ApplyMoves); Apply persists those moves first, so a plan that only
// moves resources still records the new addresses.
func Apply(ctx context.Context, p provider.Provider, b backend.Backend, s *state.State, pl *plan.Plan) error {
	resources := p.Resources()
	if len(pl.Moves) > 0 {
		s.Serial++
		if err := b.Put(s); err != nil {
			return err
		}
	}
	// outputs maps an applied resource address to its resolved attributes
	// (including "id"), so later resources can resolve references to it.
	outputs := map[string]provider.Attributes{}
	for _, r := range s.Resources {
		outputs[r.Address()] = decodeState(r)
	}
	for _, c := range pl.Changes {
		r := resources[c.Type]
		if c.ImportID != "" {
			if err := plan.ImportCollision(s, c.Type, c.Address, c.ImportID); err != nil {
				return err
			}
			desired, err := resolveRefs(c, outputs)
			if err != nil {
				return err
			}
			actual, err := r.Read(ctx, c.ImportID)
			if err != nil {
				return err
			}
			trigs, err := computeTriggers(r, desired)
			if err != nil {
				return err
			}
			if err := commit(b, s, state.Resource{
				Type: c.Type, Name: c.Name, Key: c.Key, ID: c.ImportID,
				Attributes: encodeValues(actual), Triggers: trigs, Dependencies: c.DependsOn,
			}); err != nil {
				return err
			}
			outputs[c.Address] = withID(actual, c.ImportID)
			if c.Action != provider.ActionUpdate && c.Action != provider.ActionReplace {
				continue
			}
			// else fall through to the update/replace case to reconcile to config.
		}
		switch c.Action {
		case provider.ActionNoop:
			if c.ID != "" {
				outputs[c.Address] = withID(c.Actual, c.ID)
			}
			// Nothing changed remotely, but the dependency edges may have (e.g. a
			// for_each dependency lost an instance), and a trigger may have no
			// recorded baseline yet (state written before the resource had it;
			// plan skips such replace-forcing triggers). Refresh both so state
			// never keeps an edge to something that's gone and the next content
			// change is detected; write only on a real change so a converged
			// apply doesn't churn the serial.
			st, ok := s.Find(c.Address)
			if !ok {
				continue
			}
			changed := false
			if !sameSet(st.Dependencies, c.DependsOn) {
				st.Dependencies = c.DependsOn
				changed = true
			}
			if backfillTriggers(r, c, outputs, st) {
				changed = true
			}
			if changed {
				s.Serial++
				if err := b.Put(s); err != nil {
					return err
				}
			}
			continue
		case provider.ActionCreate:
			desired, err := resolveRefs(c, outputs)
			if err != nil {
				return err
			}
			id, out, err := r.Create(ctx, desired)
			if err != nil {
				return err
			}
			trigs, err := computeTriggers(r, desired)
			if err != nil {
				return err
			}
			outputs[c.Address] = withID(out, id)
			if err := commit(b, s, stateResource(c, id, out, trigs)); err != nil {
				return err
			}
		case provider.ActionUpdate:
			desired, err := resolveRefs(c, outputs)
			if err != nil {
				return err
			}
			out, err := r.Update(ctx, c.ID, desired)
			if err != nil {
				return err
			}
			trigs, err := computeTriggers(r, desired)
			if err != nil {
				return err
			}
			outputs[c.Address] = withID(out, c.ID)
			if err := commit(b, s, stateResource(c, c.ID, out, trigs)); err != nil {
				return err
			}
		case provider.ActionReplace:
			if err := replace(ctx, r, b, s, c, outputs); err != nil {
				return err
			}
		case provider.ActionDelete:
			if err := r.Delete(ctx, c.ID); err != nil {
				return err
			}
			delete(outputs, c.Address)
			s.Remove(c.Address)
			s.Serial++
			if err := b.Put(s); err != nil {
				return err
			}
		}
	}
	return nil
}

// replace swaps the remote object at c.Address for a new one,
// create-before-destroy: create the successor, commit it to state (so state
// always names a live object and dependents resolve the new id from outputs),
// then delete the old object. Creating first means a failed create leaves the
// current object managed and untouched, and the old object is only removed
// once its successor exists. If the final delete fails, the old object is left
// behind unmanaged and the error says so; a re-run won't retry it, because
// state already points at the successor.
//
// Resources whose ids are natural keys (two objects can't coexist) would need
// destroy-before-create; none implements provider.Replacer today.
func replace(ctx context.Context, r provider.Resource, b backend.Backend, s *state.State, c plan.Change, outputs map[string]provider.Attributes) error {
	desired, err := resolveRefs(c, outputs)
	if err != nil {
		return err
	}
	id, out, err := r.Create(ctx, desired)
	if err != nil {
		return fmt.Errorf("%s: replace: creating the new object (the current one, id %s, is unchanged): %w", c.Address, c.ID, err)
	}
	trigs, err := computeTriggers(r, desired)
	if err != nil {
		return err
	}
	outputs[c.Address] = withID(out, id)
	if err := commit(b, s, stateResource(c, id, out, trigs)); err != nil {
		return err
	}
	if err := r.Delete(ctx, c.ID); err != nil && !errors.Is(err, provider.ErrNotFound) {
		return fmt.Errorf("%s: replaced by id %s, but deleting the old object (id %s) failed; it is no longer managed and must be removed by hand: %w", c.Address, id, c.ID, err)
	}
	return nil
}

// backfillTriggers adds to st any trigger the resource computes for the
// current config but state has no value for, and reports whether it did. It
// never overwrites a recorded value: a differing value is a real change, which
// plan turns into an update or replace.
func backfillTriggers(r provider.Resource, c plan.Change, outputs map[string]provider.Attributes, st *state.Resource) bool {
	if _, ok := r.(provider.Triggerable); !ok {
		return false
	}
	desired, err := resolveRefs(c, outputs)
	if err != nil {
		return false
	}
	trigs, err := computeTriggers(r, desired)
	if err != nil {
		return false
	}
	added := false
	for k, v := range trigs {
		if _, ok := st.Triggers[k]; ok {
			continue
		}
		if st.Triggers == nil {
			st.Triggers = map[string]json.RawMessage{}
		}
		st.Triggers[k] = v
		added = true
	}
	return added
}

// resolveRefs returns the change's desired attributes with referenced attributes
// filled in from already-applied outputs, in dependency order.
func resolveRefs(c plan.Change, outputs map[string]provider.Attributes) (provider.Attributes, error) {
	desired := make(provider.Attributes, len(c.Desired)+len(c.Refs))
	for k, v := range c.Desired {
		desired[k] = v
	}
	for attr, ref := range c.Refs {
		out, ok := outputs[ref.Address]
		if !ok {
			return nil, fmt.Errorf("%s: reference %s.%s cannot be resolved (dependency not applied)", c.Address, ref.Address, ref.Attr)
		}
		v, ok := out[ref.Attr]
		if !ok {
			return nil, fmt.Errorf("%s: referenced attribute %s.%s not found in applied output", c.Address, ref.Address, ref.Attr)
		}
		desired[attr] = v
	}
	return desired, nil
}

func withID(a provider.Attributes, id string) provider.Attributes {
	out := make(provider.Attributes, len(a)+1)
	for k, v := range a {
		out[k] = v
	}
	out["id"] = cty.StringVal(id)
	return out
}

func decodeState(r state.Resource) provider.Attributes {
	out := provider.Attributes{}
	for k, raw := range r.Attributes {
		if v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType); err == nil {
			out[k] = v
		}
	}
	out["id"] = cty.StringVal(r.ID)
	return out
}

func commit(b backend.Backend, s *state.State, r state.Resource) error {
	s.Upsert(r)
	s.Serial++
	return b.Put(s)
}

func computeTriggers(r provider.Resource, desired provider.Attributes) (map[string]json.RawMessage, error) {
	t, ok := r.(provider.Triggerable)
	if !ok {
		return nil, nil
	}
	vals, err := t.Triggers(desired)
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, nil
	}
	return encodeValues(vals), nil
}

// encodeValues marshals against DynamicPseudoType so the concrete cty type
// travels in the JSON wrapper ({"value":...,"type":...}). Reads use
// Unmarshal(raw, DynamicPseudoType), which requires that wrapper and recovers
// the exact type — so a list reloads as a list, not a tuple, and non-string
// references round-trip and diff correctly. Marshaling against v.Type()
// produced bare JSON that the reads could not decode (they errored and
// silently dropped the attribute).
func encodeValues(attrs provider.Attributes) map[string]json.RawMessage {
	enc := map[string]json.RawMessage{}
	for k, v := range attrs {
		raw, err := ctyjson.Marshal(v, cty.DynamicPseudoType)
		if err == nil {
			enc[k] = raw
		}
	}
	return enc
}

func stateResource(c plan.Change, id string, attrs provider.Attributes, triggers map[string]json.RawMessage) state.Resource {
	return state.Resource{
		Type: c.Type, Name: c.Name, Key: c.Key, ID: id,
		Attributes: encodeValues(attrs), Triggers: triggers, Dependencies: c.DependsOn,
	}
}

// Import adopts an existing remote resource into state: it reads the resource by
// id and writes a state entry, so plan/apply reconcile it instead of creating a
// duplicate. The address must be declared in the desired config. It never mutates
// the remote. The caller holds the state lock.
func Import(ctx context.Context, p provider.Provider, b backend.Backend, s *state.State, address, id string, desired []plan.DesiredResource) error {
	var d plan.DesiredResource
	found := false
	for _, cand := range desired {
		if cand.Address() == address {
			d, found = cand, true
			break
		}
	}
	if !found {
		for _, cand := range desired {
			if cand.Key != "" && addr.Format(cand.Type, cand.Name, "") == address {
				return fmt.Errorf("%s has for_each; import a specific instance, e.g. %s", address, addr.Format(cand.Type, cand.Name, cand.Key))
			}
		}
		return fmt.Errorf("resource %q is not declared in the configuration", address)
	}
	if existing, ok := s.Find(address); ok {
		return fmt.Errorf("resource %q is already in state (id %s)", address, existing.ID)
	}
	if err := plan.ImportCollision(s, d.Type, address, id); err != nil {
		return err
	}
	r := p.Resources()[d.Type]
	if r == nil {
		return fmt.Errorf("unknown resource type %q", d.Type)
	}
	actual, err := r.Read(ctx, id)
	if err != nil {
		if errors.Is(err, provider.ErrNotFound) {
			return fmt.Errorf("no %s found with id %q", d.Type, id)
		}
		return err
	}
	trigs, err := computeTriggers(r, d.Attributes)
	if err != nil {
		return err
	}
	return commit(b, s, state.Resource{
		Type:         d.Type,
		Name:         d.Name,
		Key:          d.Key,
		ID:           id,
		Attributes:   encodeValues(actual),
		Triggers:     trigs,
		Dependencies: d.DependsOn,
	})
}

// sameSet reports whether a and b hold the same strings, ignoring order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		if seen[x] == 0 {
			return false
		}
		seen[x]--
	}
	return true
}
