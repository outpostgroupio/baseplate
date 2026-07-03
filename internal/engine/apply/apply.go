package apply

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/state"
	"github.com/outpostgroup/baseplate/internal/state/backend"
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
			outputs[c.Address] = withID(out, id)
			if err := commit(b, s, stateResource(c, id, out)); err != nil {
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
			outputs[c.Address] = withID(out, c.ID)
			if err := commit(b, s, stateResource(c, c.ID, out)); err != nil {
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

func stateResource(c plan.Change, id string, attrs provider.Attributes) state.Resource {
	enc := map[string]json.RawMessage{}
	for k, v := range attrs {
		// Marshal against DynamicPseudoType so the concrete cty type travels in
		// the JSON wrapper ({"value":...,"type":...}). Reads use
		// Unmarshal(raw, DynamicPseudoType), which requires that wrapper and
		// recovers the exact type — so a list reloads as a list, not a tuple, and
		// non-string references round-trip and diff correctly. Marshaling against
		// v.Type() produced bare JSON that the reads could not decode (they
		// errored and silently dropped the attribute).
		raw, err := ctyjson.Marshal(v, cty.DynamicPseudoType)
		if err == nil {
			enc[k] = raw
		}
	}
	return state.Resource{Type: c.Type, Name: name(c.Address), ID: id, Attributes: enc, Dependencies: c.DependsOn}
}

func name(address string) string {
	for i := 0; i < len(address); i++ {
		if address[i] == '.' {
			return address[i+1:]
		}
	}
	return address
}
