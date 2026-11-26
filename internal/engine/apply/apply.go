package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
			desired, err := resolveRefs(c, outputs, resources)
			if err != nil {
				return err
			}
			actual, err := provider.ReadForImport(ctx, r, c.ImportID, desired)
			if err != nil {
				return provider.ResourceError(c.Address, c.Type, r, err)
			}
			trigs, err := triggersKeeping(r, desired, nil)
			if err != nil {
				return err
			}
			// Plan diffed unreadable triggers against "unverified", so an
			// update follows and records the real values.
			trigs, _ = markUnverified(r, trigs)
			if err := commit(b, s, state.Resource{
				Type: c.Type, Name: c.Name, Key: c.Key, ID: c.ImportID,
				Attributes: encodeValues(actual), Triggers: trigs, Dependencies: c.DependsOn,
				SensitiveAttributes: c.SensitiveAttrs,
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
			st, ok := s.Find(c.Address)
			if c.ID != "" {
				actual := c.Actual
				if ok {
					actual = keepOutputs(r, actual, st)
				}
				outputs[c.Address] = withID(actual, c.ID)
			}
			// Nothing changed remotely, but the dependency edges may have (e.g. a
			// for_each dependency lost an instance), so may the sensitive
			// attributes, a trigger may have no
			// recorded baseline yet (state written before the resource had it;
			// plan skips such replace-forcing triggers), and Read may report a
			// computed output that state doesn't have yet (provider.Outputter).
			// Refresh them so state never keeps an edge to something that's
			// gone, the next content change is detected and outputs are
			// recorded; write only on a real change so a converged apply
			// doesn't churn the serial.
			if !ok {
				continue
			}
			changed := recordOutputs(r, c.Actual, st)
			if !sameSet(st.Dependencies, c.DependsOn) {
				st.Dependencies = c.DependsOn
				changed = true
			}
			// The config can make an attribute sensitive without changing
			// its value; show must hide it from then on.
			if !sameSet(st.SensitiveAttributes, c.SensitiveAttrs) {
				st.SensitiveAttributes = c.SensitiveAttrs
				changed = true
			}
			if len(c.BackfillTriggers) > 0 && backfillTriggers(r, c, outputs, st) {
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
			desired, err := resolveRefs(c, outputs, resources)
			if err != nil {
				return err
			}
			// Triggers first: a failure after Create would orphan the new
			// remote object (created, but never recorded in state).
			trigs, err := computeTriggers(r, desired)
			if err != nil {
				return err
			}
			id, out, err := r.Create(ctx, desired)
			if err != nil {
				return provider.ResourceError(c.Address, c.Type, r, err)
			}
			outputs[c.Address] = withID(out, id)
			if err := commit(b, s, stateResource(c, id, out, trigs)); err != nil {
				return err
			}
		case provider.ActionUpdate:
			desired, err := resolveRefs(c, outputs, resources)
			if err != nil {
				return err
			}
			var prior map[string]json.RawMessage
			if st, ok := s.Find(c.Address); ok {
				prior = st.Triggers
			}
			trigs, err := triggersKeeping(r, desired, prior)
			if err != nil {
				return err
			}
			out, err := update(ctx, r, c.ID, desired, prior)
			if err != nil {
				return provider.ResourceError(c.Address, c.Type, r, err)
			}
			if st, ok := s.Find(c.Address); ok {
				out = keepOutputs(r, out, st)
			}
			outputs[c.Address] = withID(out, c.ID)
			if err := commit(b, s, stateResource(c, c.ID, out, trigs)); err != nil {
				return err
			}
		case provider.ActionReplace:
			if err := replace(ctx, r, b, s, c, outputs, resources); err != nil {
				return err
			}
		case provider.ActionDelete:
			if err := r.Delete(ctx, c.ID); err != nil {
				return provider.ResourceError(c.Address, c.Type, r, err)
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
// then delete the old object, or, for a resource that abandons on replace
// (c.AbandonsOld), leave it in place untouched. Creating first means a failed
// create leaves the current object managed and untouched. If the final delete
// fails, the old object is left behind unmanaged and the error says so; a
// re-run won't retry it, because state already points at the successor.
//
// Resources whose ids are natural keys (two objects can't coexist) would need
// destroy-before-create; none implements provider.Replacer today.
func replace(ctx context.Context, r provider.Resource, b backend.Backend, s *state.State, c plan.Change, outputs map[string]provider.Attributes, resources map[string]provider.Resource) error {
	desired, err := resolveRefs(c, outputs, resources)
	if err != nil {
		return err
	}
	// Triggers first, before any remote mutation: a failure after Create would
	// orphan the successor.
	trigs, err := computeTriggers(r, desired)
	if err != nil {
		return err
	}
	id, out, err := r.Create(ctx, desired)
	if err != nil {
		return provider.ResourceError(c.Address, c.Type, r, fmt.Errorf("replace: creating the new object (the current one, id %s, is unchanged): %w", c.ID, err))
	}
	outputs[c.Address] = withID(out, id)
	if c.AbandonsOld {
		// The old object stays on the platform, no longer managed. Record it,
		// in the same write as its successor, for `baseplate cleanup`.
		s.Retired = append(s.Retired, state.Retired{Type: c.Type, ID: c.ID, Address: c.Address, ReplacedBy: id, RetiredAt: now().UTC()})
	}
	if err := commit(b, s, stateResource(c, id, out, trigs)); err != nil {
		return err
	}
	if c.AbandonsOld {
		return nil
	}
	if err := r.Delete(ctx, c.ID); err != nil && !errors.Is(err, provider.ErrNotFound) {
		return provider.ResourceError(c.Address, c.Type, r, fmt.Errorf("replaced by id %s, but deleting the old object (id %s) failed; it is no longer managed and must be removed by hand: %w", id, c.ID, err))
	}
	return nil
}

// now is time.Now, replaceable in tests.
var now = time.Now

// update calls the resource's UpdateWithTriggers when it has one (passing the
// triggers recorded before this update), else Update.
func update(ctx context.Context, r provider.Resource, id string, desired provider.Attributes, prior map[string]json.RawMessage) (provider.Attributes, error) {
	if u, ok := r.(provider.TriggerAwareUpdater); ok {
		return u.UpdateWithTriggers(ctx, id, desired, decodeRaw(prior))
	}
	return r.Update(ctx, id, desired)
}

// triggersKeeping computes the triggers for desired. When an input is missing
// (provider.TriggerUnavailableError), the triggers that could not be computed
// keep their prior recorded values, so an update or import that doesn't need
// the input (e.g. a metadata edit on a machine without the source file)
// neither fails nor loses the baseline.
func triggersKeeping(r provider.Resource, desired provider.Attributes, prior map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	t, ok := r.(provider.Triggerable)
	if !ok {
		return nil, nil
	}
	vals, err := t.Triggers(desired)
	var unavailable *provider.TriggerUnavailableError
	if err != nil && !errors.As(err, &unavailable) {
		return nil, err
	}
	out := encodeValues(vals)
	for k, v := range prior {
		if _, ok := out[k]; !ok && unavailable != nil {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// backfillTriggers records the triggers plan adopted as a baseline
// (c.BackfillTriggers: no value in state) and reports whether it did. It never
// overwrites a recorded value: a differing value is a real change, which plan
// turns into an update or replace.
func backfillTriggers(r provider.Resource, c plan.Change, outputs map[string]provider.Attributes, st *state.Resource) bool {
	desired, err := resolveRefs(c, outputs, nil)
	if err != nil {
		return false
	}
	trigs, err := triggersKeeping(r, desired, nil)
	if err != nil {
		return false
	}
	added := false
	for _, k := range c.BackfillTriggers {
		v, ok := trigs[k]
		if !ok {
			continue
		}
		if _, recorded := st.Triggers[k]; recorded {
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

// decodeRaw decodes a state map of {value,type}-wrapped JSON values.
func decodeRaw(m map[string]json.RawMessage) map[string]cty.Value {
	out := map[string]cty.Value{}
	for k, raw := range m {
		if v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType); err == nil {
			out[k] = v
		}
	}
	return out
}

// resolveRefs returns the change's desired attributes with referenced attributes
// filled in from already-applied outputs, in dependency order.
func resolveRefs(c plan.Change, outputs map[string]provider.Attributes, resources map[string]provider.Resource) (provider.Attributes, error) {
	desired := make(provider.Attributes, len(c.Desired)+len(c.Refs))
	for k, v := range c.Desired {
		desired[k] = v
	}
	for attr, ref := range c.Refs {
		v, err := outputRef(c.Address, ref, outputs, resources)
		if err != nil {
			return nil, err
		}
		desired[attr] = v
	}
	for attr, refs := range c.PathRefs {
		var lookupErr error
		filled, err := plan.FillRefs(desired[attr], refs, func(ref provider.Ref) (cty.Value, bool) {
			v, err := outputRef(c.Address, ref, outputs, resources)
			if err != nil && lookupErr == nil {
				lookupErr = err
			}
			return v, err == nil
		})
		if lookupErr != nil {
			return nil, lookupErr
		}
		if err != nil {
			return nil, fmt.Errorf("%s: attribute %q: %w", c.Address, attr, err)
		}
		desired[attr] = filled
	}
	return desired, nil
}

// outputRef returns the value of ref from the outputs of resources already
// applied; address is the referencing resource, for the error.
func outputRef(address string, ref provider.Ref, outputs map[string]provider.Attributes, resources map[string]provider.Resource) (cty.Value, error) {
	out, ok := outputs[ref.Address]
	if !ok {
		return cty.NilVal, fmt.Errorf("%s: reference %s.%s cannot be resolved (dependency not applied)", address, ref.Address, ref.Attr)
	}
	v, ok := out[ref.Attr]
	if !ok {
		if isOutput(resources, ref) {
			return cty.NilVal, fmt.Errorf("%s: references %s.%s, which is not set: the provider couldn't determine it for this object, or it doesn't apply to it (see the warnings, and the resource reference for when it is set)", address, ref.Address, ref.Attr)
		}
		return cty.NilVal, fmt.Errorf("%s: referenced attribute %s.%s not found in applied output", address, ref.Address, ref.Attr)
	}
	return v, nil
}

// isOutput reports whether ref names a computed output (provider.Outputter)
// of the referenced resource's type.
func isOutput(resources map[string]provider.Resource, ref provider.Ref) bool {
	typ, _, _, _, err := addr.Parse(ref.Address)
	if err != nil {
		return false
	}
	o, ok := resources[typ].(provider.Outputter)
	if !ok {
		return false
	}
	_, ok = o.Outputs()[ref.Attr]
	return ok
}

// keepOutputs returns attrs with any computed output (provider.Outputter)
// that attrs lacks but st records filled in from st. An output is fixed for
// the object's id, so a lookup that failed this time doesn't lose it.
func keepOutputs(r provider.Resource, attrs provider.Attributes, st *state.Resource) provider.Attributes {
	o, ok := r.(provider.Outputter)
	if !ok {
		return attrs
	}
	var out provider.Attributes
	for k := range o.Outputs() {
		if _, has := attrs[k]; has {
			continue
		}
		raw, recorded := st.Attributes[k]
		if !recorded {
			continue
		}
		v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType)
		if err != nil {
			continue
		}
		if out == nil {
			out = make(provider.Attributes, len(attrs)+1)
			for ak, av := range attrs {
				out[ak] = av
			}
		}
		out[k] = v
	}
	if out == nil {
		return attrs
	}
	return out
}

// recordOutputs writes the computed outputs (provider.Outputter) that actual
// reports into st where they differ from what st records, and reports whether
// it changed anything. An output actual lacks is left as recorded.
func recordOutputs(r provider.Resource, actual provider.Attributes, st *state.Resource) bool {
	o, ok := r.(provider.Outputter)
	if !ok {
		return false
	}
	changed := false
	for k := range o.Outputs() {
		v, has := actual[k]
		if !has || !v.IsKnown() {
			continue
		}
		enc := encodeValues(provider.Attributes{k: v})[k]
		if enc == nil || string(enc) == string(st.Attributes[k]) {
			continue
		}
		if st.Attributes == nil {
			st.Attributes = map[string]json.RawMessage{}
		}
		st.Attributes[k] = enc
		changed = true
	}
	return changed
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
// the exact type, so a list reloads as a list, not a tuple, and non-string
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
		SensitiveAttributes: c.SensitiveAttrs,
	}
}

// ImportOptions tunes Import and ImportAll.
type ImportOptions struct {
	// AssumeCurrent records the config's values for triggers the platform
	// can't report (provider.UnreadableTriggers), as if the live object were
	// known to match the config, instead of marking them unverified.
	AssumeCurrent bool
}

// Import adopts an existing remote resource into state: it reads the resource by
// id and writes a state entry, so plan/apply reconcile it instead of creating a
// duplicate. The address must be declared in the desired config. It never mutates
// the remote. The caller holds the state lock.
func Import(ctx context.Context, p provider.Provider, b backend.Backend, s *state.State, address, id string, desired []plan.DesiredResource) error {
	return ImportWithOptions(ctx, p, b, s, address, id, desired, ImportOptions{})
}

// ImportWithOptions is Import with options.
func ImportWithOptions(ctx context.Context, p provider.Provider, b backend.Backend, s *state.State, address, id string, desired []plan.DesiredResource, opts ImportOptions) error {
	r, err := adopt(ctx, p, s, address, id, desired, opts)
	if err != nil {
		return err
	}
	return commit(b, s, r)
}

// ImportAll runs every import block whose target isn't in state yet, as
// Import does for one address, and writes state once at the end: either every
// pending import is recorded or none is. It returns the blocks it imported. It
// never mutates the remote. The caller holds the state lock.
func ImportAll(ctx context.Context, p provider.Provider, b backend.Backend, s *state.State, imports []plan.ImportBlock, desired []plan.DesiredResource, opts ImportOptions) ([]plan.ImportBlock, error) {
	seen := map[string]bool{}
	var done []plan.ImportBlock
	for _, imp := range imports {
		if seen[imp.Address] {
			return nil, fmt.Errorf("duplicate import block for %q", imp.Address)
		}
		seen[imp.Address] = true
		if st, ok := s.Find(imp.Address); ok {
			if st.ID != imp.ID {
				return nil, fmt.Errorf("import %s: already in state with id %s, not %s", imp.Address, st.ID, imp.ID)
			}
			continue // already imported
		}
		r, err := adopt(ctx, p, s, imp.Address, imp.ID, desired, opts)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", imp.Address, err)
		}
		// Upsert now so a later block that reuses this id is caught as a
		// collision; nothing is written unless every block succeeds.
		s.Upsert(r)
		done = append(done, imp)
	}
	if len(done) == 0 {
		return nil, nil
	}
	s.Serial++
	if err := b.Put(s); err != nil {
		return nil, err
	}
	return done, nil
}

// adopt reads the remote object id for the declared address and returns the
// state entry an import records for it, without writing state.
func adopt(ctx context.Context, p provider.Provider, s *state.State, address, id string, desired []plan.DesiredResource, opts ImportOptions) (state.Resource, error) {
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
				return state.Resource{}, fmt.Errorf("%s has for_each; import a specific instance, e.g. %s", address, addr.Format(cand.Type, cand.Name, cand.Key))
			}
		}
		return state.Resource{}, fmt.Errorf("resource %q is not declared in the configuration", address)
	}
	if existing, ok := s.Find(address); ok {
		return state.Resource{}, fmt.Errorf("resource %q is already in state (id %s)", address, existing.ID)
	}
	if err := plan.ImportCollision(s, d.Type, address, id); err != nil {
		return state.Resource{}, err
	}
	r := p.Resources()[d.Type]
	if r == nil {
		return state.Resource{}, fmt.Errorf("unknown resource type %q", d.Type)
	}
	actual, err := provider.ReadForImport(ctx, r, id, d.Attributes)
	if err != nil {
		if errors.Is(err, provider.ErrNotFound) {
			return state.Resource{}, fmt.Errorf("no %s found with id %q", d.Type, id)
		}
		return state.Resource{}, provider.ResourceError(address, d.Type, r, err)
	}
	trigs, err := triggersKeeping(r, d.Attributes, nil)
	if err != nil {
		return state.Resource{}, err
	}
	if !opts.AssumeCurrent {
		var unverified []string
		if trigs, unverified = markUnverified(r, trigs); len(unverified) > 0 {
			provider.Warn(ctx, fmt.Sprintf("%s: %s can't be read back from the platform, so it is recorded as unverified and the next apply updates the object from the config. If the live object already matches the config, import with --assume-current instead.", address, strings.Join(unverified, ", ")))
		}
	}
	return state.Resource{
		Type:         d.Type,
		Name:         d.Name,
		Key:          d.Key,
		ID:           id,
		Attributes:   encodeValues(actual),
		Triggers:     trigs,
		Dependencies: d.DependsOn,
		// The import's attributes are read back, but they still show what the
		// config's sensitive values set.
		SensitiveAttributes: d.SensitiveAttrs,
	}, nil
}

// markUnverified replaces an import's unreadable triggers
// (provider.UnreadableTriggers) with provider.UnverifiedTrigger, so the next
// plan updates the object rather than assuming the live content matches the
// config. It returns the names it marked.
func markUnverified(r provider.Resource, trigs map[string]json.RawMessage) (map[string]json.RawMessage, []string) {
	u, ok := r.(provider.UnreadableTriggers)
	if !ok {
		return trigs, nil
	}
	names := u.UnreadableTriggers()
	if len(names) == 0 {
		return trigs, nil
	}
	vals := provider.Attributes{}
	for _, k := range names {
		vals[k] = cty.StringVal(provider.UnverifiedTrigger)
	}
	if trigs == nil {
		trigs = map[string]json.RawMessage{}
	}
	for k, v := range encodeValues(vals) {
		trigs[k] = v
	}
	return trigs, names
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
