package apply

import (
	"fmt"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// RecordOutputs records the config's output values in state after an apply,
// writing it when they changed. values are evaluated against the applied
// state (nil after a destroy, which removes them all). A value that still
// isn't wholly known, because --target left a resource it uses unapplied,
// keeps the value recorded before.
func RecordOutputs(b backend.Backend, s *state.State, values map[string]plan.OutputValue) error {
	outputs := map[string]state.Output{}
	for name, ov := range values {
		if !ov.Value.IsWhollyKnown() {
			if prev, ok := s.Outputs[name]; ok {
				outputs[name] = prev
			}
			continue
		}
		o, err := state.NewOutput(ov.Value, ov.Sensitive)
		if err != nil {
			return fmt.Errorf("output %q: %w", name, err)
		}
		outputs[name] = o
	}
	if !s.SetOutputs(outputs) {
		return nil
	}
	s.Serial++
	return b.Put(s)
}
