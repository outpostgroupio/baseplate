// Package builtin holds resources every provider includes, which live only in
// Baseplate's state.
package builtin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// DataType is the type name of the Data resource.
const DataType = "baseplate_data"

// Data returns baseplate_data, Baseplate's equivalent of Terraform's
// terraform_data: it stores a value in state and nothing else. Its use is as a
// lifecycle.replace_triggered_by target, which replaces another resource when
// the stored value changes, e.g. a hash of a file whose changes should give an
// asset a new id.
func Data() provider.Resource { return data{} }

type data struct{}

var (
	_ provider.StateOnly  = data{}
	_ provider.Replacer   = data{}
	_ provider.Outputter  = data{}
	_ provider.Documented = data{}
)

func (data) Type() string { return DataType }

func (data) StateOnly() {}

func (data) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Stores a value in state, for `lifecycle { replace_triggered_by }`. The equivalent of Terraform's `terraform_data`; it makes no requests.",
		Attributes: map[string]string{
			"input":            "Any value. A change updates the resource, which replaces any resource that lists it in `replace_triggered_by`.",
			"triggers_replace": "Any value. A change replaces this resource (with a new `id`).",
		},
		Outputs: map[string]string{
			"output": "The value of `input`.",
		},
		Notes: "To replace a resource whenever a value changes, store the value in a `baseplate_data` and list it in the resource's `lifecycle { replace_triggered_by = [...] }`; with `for_each`, give both the same keys and reference `baseplate_data.<name>[each.key]`. Creating a `baseplate_data` doesn't trigger a replace, so adding one for existing resources replaces nothing. It can't be imported.",
	}
}

func (data) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"input":            &hcldec.AttrSpec{Name: "input", Type: cty.DynamicPseudoType},
		"triggers_replace": &hcldec.AttrSpec{Name: "triggers_replace", Type: cty.DynamicPseudoType},
	}
}

func (data) Outputs() map[string]cty.Type {
	return map[string]cty.Type{"output": cty.DynamicPseudoType}
}

func (data) Computed() []string { return []string{"id", "output"} }

func (data) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (data) RequiresReplace(_, _ provider.Attributes) []string {
	return []string{"triggers_replace"}
}

// ReplaceAbandons is true: there is no remote object to delete.
func (data) ReplaceAbandons() bool { return false }

func (data) Create(_ context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), attrs(desired), nil
}

// Read is never called: the engine uses state for a provider.StateOnly
// resource.
func (data) Read(context.Context, string) (provider.Attributes, error) {
	return nil, errors.New("baseplate_data exists only in state, so it can't be read or imported")
}

func (data) Update(_ context.Context, _ string, desired provider.Attributes) (provider.Attributes, error) {
	return attrs(desired), nil
}

func (data) Delete(context.Context, string) error { return nil }

// attrs is desired plus output, which mirrors input.
func attrs(desired provider.Attributes) provider.Attributes {
	out := provider.Attributes{}
	for k, v := range desired {
		out[k] = v
	}
	if v, ok := desired["input"]; ok {
		out["output"] = v
	} else {
		out["output"] = cty.NullVal(cty.DynamicPseudoType)
	}
	return out
}
