package state

import (
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// NewOutput records v, which must be wholly known.
func NewOutput(v cty.Value, sensitive bool) (Output, error) {
	ty, err := ctyjson.MarshalType(v.Type())
	if err != nil {
		return Output{}, err
	}
	val, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return Output{}, err
	}
	return Output{Value: val, Type: ty, Sensitive: sensitive}, nil
}

// CtyValue decodes the recorded value.
func (o Output) CtyValue() (cty.Value, error) {
	ty, err := ctyjson.UnmarshalType(o.Type)
	if err != nil {
		return cty.NilVal, fmt.Errorf("output type: %w", err)
	}
	return ctyjson.Unmarshal(o.Value, ty)
}

// OutputValues decodes outputs into one object value, as
// baseplate_remote_state's `outputs` attribute exposes them.
func OutputValues(outputs map[string]Output) (map[string]cty.Value, error) {
	out := make(map[string]cty.Value, len(outputs))
	for name, o := range outputs {
		v, err := o.CtyValue()
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}

// equalOutput reports whether a and b record the same value. The JSON is
// compared after decoding, so whitespace differences don't count.
func equalOutput(a, b Output) bool {
	if a.Sensitive != b.Sensitive {
		return false
	}
	var av, bv, at, bt any
	if json.Unmarshal(a.Value, &av) != nil || json.Unmarshal(b.Value, &bv) != nil ||
		json.Unmarshal(a.Type, &at) != nil || json.Unmarshal(b.Type, &bt) != nil {
		return false
	}
	aj, _ := json.Marshal([]any{av, at})
	bj, _ := json.Marshal([]any{bv, bt})
	return string(aj) == string(bj)
}

// SetOutputs replaces the recorded outputs and reports whether they changed.
func (s *State) SetOutputs(outputs map[string]Output) bool {
	if len(outputs) == 0 {
		outputs = nil
	}
	changed := len(outputs) != len(s.Outputs)
	for name, o := range outputs {
		if prev, ok := s.Outputs[name]; !ok || !equalOutput(prev, o) {
			changed = true
		}
	}
	s.Outputs = outputs
	return changed
}
