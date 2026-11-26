package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// OutputValue is an output block's value. Value is unknown, wholly or in
// part, when it depends on a resource this plan creates or replaces.
type OutputValue struct {
	Value     cty.Value
	Sensitive bool
}

// OutputChange is a change to a recorded output value: Create, Update or
// Delete. Old is null for a create, New for a delete.
type OutputChange struct {
	Name      string
	Action    provider.ChangeAction
	Old, New  cty.Value
	Sensitive bool
}

// JSONOutputChange is an OutputChange in plan --json. Sensitive values are
// redacted.
type JSONOutputChange struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	Old    string `json:"old,omitempty"`
	New    string `json:"new,omitempty"`
}

// OutputChanges compares the outputs recorded in state with the config's
// values (nil when destroying, which removes them all), sorted by name.
func OutputChanges(recorded map[string]state.Output, values map[string]OutputValue) ([]OutputChange, error) {
	var out []OutputChange
	for name, ov := range values {
		rec, ok := recorded[name]
		if !ok {
			out = append(out, OutputChange{Name: name, Action: provider.ActionCreate, Old: cty.NullVal(cty.DynamicPseudoType), New: ov.Value, Sensitive: ov.Sensitive})
			continue
		}
		old, err := rec.CtyValue()
		if err != nil {
			return nil, fmt.Errorf("output %q in state: %w", name, err)
		}
		if old.RawEquals(ov.Value) || sameJSON(old, ov.Value) {
			if rec.Sensitive == ov.Sensitive {
				continue
			}
		}
		out = append(out, OutputChange{Name: name, Action: provider.ActionUpdate, Old: old, New: ov.Value, Sensitive: ov.Sensitive || rec.Sensitive})
	}
	for name, rec := range recorded {
		if _, ok := values[name]; ok {
			continue
		}
		old, err := rec.CtyValue()
		if err != nil {
			return nil, fmt.Errorf("output %q in state: %w", name, err)
		}
		out = append(out, OutputChange{Name: name, Action: provider.ActionDelete, Old: old, New: cty.NullVal(cty.DynamicPseudoType), Sensitive: rec.Sensitive})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Pending returns the addresses this plan gives a new remote object (create or
// replace). Their attributes, the id above all, aren't known until apply.
func (p *Plan) Pending() map[string]bool {
	out := map[string]bool{}
	for a, ok := range p.pending {
		if ok {
			out[a] = true
		}
	}
	return out
}

// InstanceValues returns the value each resource instance will have after
// this plan, for evaluating output blocks: its attributes and id from state,
// with an update's configured attributes laid over them. An instance the plan
// creates, replaces or destroys is left out, so it reads as unknown.
func (p *Plan) InstanceValues(s *state.State) map[string]cty.Value {
	skip := p.Pending()
	desired := map[string]provider.Attributes{}
	for _, c := range p.Changes {
		switch c.Action {
		case provider.ActionDelete:
			skip[c.Address] = true
		case provider.ActionUpdate:
			desired[c.Address] = c.Desired
		}
	}
	out := map[string]cty.Value{}
	for _, r := range s.Resources {
		a := r.Address()
		if skip[a] {
			continue
		}
		out[a] = instanceValue(r, desired[a])
	}
	return out
}

// StateInstanceValues is InstanceValues for state as it is, e.g. after apply.
func StateInstanceValues(s *state.State) map[string]cty.Value {
	out := map[string]cty.Value{}
	for _, r := range s.Resources {
		out[r.Address()] = instanceValue(r, nil)
	}
	return out
}

func instanceValue(r state.Resource, overlay provider.Attributes) cty.Value {
	attrs := map[string]cty.Value{}
	for k, raw := range r.Attributes {
		if v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType); err == nil {
			attrs[k] = v
		}
	}
	for k, v := range overlay {
		if v.IsWhollyKnown() {
			attrs[k] = v
		}
	}
	attrs["id"] = cty.StringVal(r.ID)
	return cty.ObjectVal(attrs)
}

// JSONOutputs renders the output changes for plan --json; nil when there
// are none.
func (p *Plan) JSONOutputs() []JSONOutputChange {
	if len(p.Outputs) == 0 {
		return nil
	}
	out := make([]JSONOutputChange, len(p.Outputs))
	for i, c := range p.Outputs {
		jc := JSONOutputChange{Name: c.Name, Action: c.Action.String()}
		switch {
		case c.Sensitive:
			if c.Action != provider.ActionCreate {
				jc.Old = "(sensitive value)"
			}
			if c.Action != provider.ActionDelete {
				jc.New = "(sensitive value)"
			}
		default:
			if c.Action != provider.ActionCreate {
				jc.Old = ctyString(c.Old)
			}
			if c.Action != provider.ActionDelete {
				jc.New = ctyString(c.New)
			}
		}
		out[i] = jc
	}
	return out
}

// renderOutputs is the "Changes to Outputs" section of RenderText.
func (p *Plan) renderOutputs() string {
	if len(p.Outputs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nChanges to Outputs:\n")
	show := func(c OutputChange, v cty.Value) string {
		if c.Sensitive {
			return "(sensitive value)"
		}
		return OutputString(v)
	}
	for _, c := range p.Outputs {
		switch c.Action {
		case provider.ActionCreate:
			fmt.Fprintf(&b, "  + %s = %s\n", c.Name, show(c, c.New))
		case provider.ActionDelete:
			fmt.Fprintf(&b, "  - %s = %s\n", c.Name, show(c, c.Old))
		default:
			fmt.Fprintf(&b, "  ~ %s: %s => %s\n", c.Name, show(c, c.Old), show(c, c.New))
		}
	}
	return b.String()
}

// OutputString renders an output value as Terraform does: a string quoted,
// anything else as in the plan.
func OutputString(v cty.Value) string {
	if v.IsKnown() && !v.IsNull() && v.Type() == cty.String {
		return fmt.Sprintf("%q", v.AsString())
	}
	return ctyString(v)
}
