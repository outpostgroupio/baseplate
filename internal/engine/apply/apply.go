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

func Apply(ctx context.Context, p provider.Provider, b backend.Backend, s *state.State, pl *plan.Plan) error {
	resources := p.Resources()
	// outputs maps an applied resource address to its resolved attributes
	// (including "id"), so later resources can resolve references to it.
	outputs := map[string]provider.Attributes{}
	for _, r := range s.Resources {
		outputs[r.Address()] = decodeState(r)
	}
	for _, c := range pl.Changes {
		r := resources[c.Type]
		if c.ImportID != "" {
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
			if c.Action != provider.ActionUpdate {
				continue
			}
			// else fall through to the ActionUpdate case to reconcile to config.
		}
		switch c.Action {
		case provider.ActionNoop:
			if c.ID != "" {
				outputs[c.Address] = withID(c.Actual, c.ID)
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
