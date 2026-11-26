package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/cli/output"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox"
	"github.com/outpostgroupio/baseplate/internal/state"
)

type showResource struct {
	Address    string                     `json:"address"`
	Type       string                     `json:"type"`
	Name       string                     `json:"name"`
	Key        string                     `json:"key,omitempty"`
	ID         string                     `json:"id"`
	Attributes map[string]json.RawMessage `json:"attributes"`
	// Triggers are the change-detection values recorded for the resource,
	// such as roblox_asset's content_hash.
	Triggers map[string]json.RawMessage `json:"triggers,omitempty"`
}

type showResult struct {
	Serial    int            `json:"serial"`
	Lineage   string         `json:"lineage"`
	Resources []showResource `json:"resources"`
}

// showRun prints the state inventory: every managed resource with its id and
// attributes, or just the resource(s) matching address (a whole resource or
// one instance, parsed like --target). It reads state through the configured
// backend without taking the lock, makes no provider calls and needs no API
// key; the config is only consulted for its backend block.
func showRun(p provider.Provider, cfgPath, statePath, address string, format output.Format, s streams) error {
	tgt, err := parseTarget("show", address)
	if err != nil {
		return err
	}
	b, _, err := openBackend(cfgPath, statePath)
	if err != nil {
		return err
	}
	st, err := b.Get()
	if err != nil {
		return err
	}
	res := showResult{Serial: st.Serial, Lineage: st.Lineage, Resources: []showResource{}}
	sensitive := map[string]map[string]bool{} // per resource type
	for _, r := range st.Resources {
		if tgt != nil && !tgt.matches(r.Type, r.Name, r.Key) {
			continue
		}
		sens, ok := sensitive[r.Type]
		if !ok {
			sens = sensitiveFor(p, r.Type)
			sensitive[r.Type] = sens
		}
		if len(r.SensitiveAttributes) > 0 {
			merged := map[string]bool{}
			for k := range sens {
				merged[k] = true
			}
			for _, k := range r.SensitiveAttributes {
				merged[k] = true
			}
			sens = merged
		}
		res.Resources = append(res.Resources, toShowResource(r, sens))
	}
	sort.Slice(res.Resources, func(i, j int) bool { return res.Resources[i].Address < res.Resources[j].Address })
	if tgt != nil && len(res.Resources) == 0 {
		return fmt.Errorf("show: no resource matching %s in state", address)
	}
	return output.Emit(s.out, format, "show", renderShow(res, tgt != nil), res)
}

// sensitiveFor returns the sensitive attribute names for a resource type.
// Sensitive values are never written to state, but show filters them anyway
// as a guard. The type is looked up in the active provider and, failing that,
// in the Roblox schema (so the guard holds without ROBLOX_API_KEY too).
func sensitiveFor(p provider.Provider, typ string) map[string]bool {
	r, ok := p.Resources()[typ]
	if !ok {
		r, ok = roblox.SchemaProvider().Resources()[typ]
	}
	out := map[string]bool{}
	if sr, isSensitive := r.(provider.Sensitive); ok && isSensitive {
		for _, k := range sr.Sensitive() {
			out[k] = true
		}
	}
	return out
}

// toShowResource converts a state entry for output: attribute values are
// unwrapped from state's {value,type} encoding into plain JSON, and sensitive
// attributes are dropped.
func toShowResource(r state.Resource, sensitive map[string]bool) showResource {
	attrs := map[string]json.RawMessage{}
	for k, raw := range r.Attributes {
		if sensitive[k] {
			continue
		}
		attrs[k] = plainJSON(raw)
	}
	var trigs map[string]json.RawMessage
	for k, raw := range r.Triggers {
		if trigs == nil {
			trigs = map[string]json.RawMessage{}
		}
		trigs[k] = plainJSON(raw)
	}
	return showResource{Address: r.Address(), Type: r.Type, Name: r.Name, Key: r.Key, ID: r.ID, Attributes: attrs, Triggers: trigs}
}

// plainJSON unwraps a state attribute ({"value":…,"type":…}) to its bare JSON
// value. Anything that doesn't decode as the wrapper is passed through as-is.
func plainJSON(raw json.RawMessage) json.RawMessage {
	v, err := ctyjson.Unmarshal(raw, cty.DynamicPseudoType)
	if err != nil {
		return raw
	}
	b, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return raw
	}
	return b
}

// renderShow is the text form: one `address  id` line per resource, or, when
// showing a specific address, each matching resource's attributes.
func renderShow(res showResult, detail bool) string {
	if len(res.Resources) == 0 {
		return "No resources in state.\n"
	}
	var b strings.Builder
	if !detail {
		width := 0
		for _, r := range res.Resources {
			width = max(width, len(r.Address))
		}
		for _, r := range res.Resources {
			fmt.Fprintf(&b, "%-*s  %s\n", width, r.Address, r.ID)
		}
		return b.String()
	}
	for i, r := range res.Resources {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s\n", r.Address)
		keys := []string{"id"}
		for k := range r.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys[1:])
		width := 0
		for _, k := range keys {
			width = max(width, len(k))
		}
		for _, k := range keys {
			v := r.ID
			if k != "id" {
				v = textValue(r.Attributes[k])
			}
			fmt.Fprintf(&b, "    %-*s = %s\n", width, k, v)
		}
	}
	return b.String()
}

// textValue renders a plain JSON value for text output: strings unquoted,
// everything else as compact JSON.
func textValue(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
