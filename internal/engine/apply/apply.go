package apply

import (
	"context"
	"encoding/json"

	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/state"
	"github.com/outpostgroup/baseplate/internal/state/backend"
)

func Apply(ctx context.Context, p provider.Provider, b backend.Backend, s *state.State, pl *plan.Plan) error {
	resources := p.Resources()
	for _, c := range pl.Changes {
		r := resources[c.Type]
		switch c.Action {
		case provider.ActionNoop:
			continue
		case provider.ActionCreate:
			id, out, err := r.Create(ctx, c.Desired)
			if err != nil {
				return err
			}
			if err := commit(b, s, stateResource(c, id, out)); err != nil {
				return err
			}
		case provider.ActionUpdate:
			out, err := r.Update(ctx, c.ID, c.Desired)
			if err != nil {
				return err
			}
			if err := commit(b, s, stateResource(c, c.ID, out)); err != nil {
				return err
			}
		case provider.ActionDelete:
			if err := r.Delete(ctx, c.ID); err != nil {
				return err
			}
			s.Remove(c.Address)
			s.Serial++
			if err := b.Put(s); err != nil {
				return err
			}
		}
	}
	return nil
}

func commit(b backend.Backend, s *state.State, r state.Resource) error {
	s.Upsert(r)
	s.Serial++
	return b.Put(s)
}

func stateResource(c plan.Change, id string, attrs provider.Attributes) state.Resource {
	enc := map[string]json.RawMessage{}
	for k, v := range attrs {
		raw, err := ctyjson.Marshal(v, v.Type())
		if err == nil {
			enc[k] = raw
		}
	}
	return state.Resource{Type: c.Type, Name: name(c.Address), ID: id, Attributes: enc}
}

func name(address string) string {
	for i := 0; i < len(address); i++ {
		if address[i] == '.' {
			return address[i+1:]
		}
	}
	return address
}
