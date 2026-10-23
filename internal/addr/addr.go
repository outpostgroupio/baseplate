// Package addr is the single source of truth for rendering and parsing resource
// instance addresses: `type.name` for a singular resource and `type.name["key"]`
// for one instance of a for_each resource. Config, plan, state and the CLI all
// go through it, so an address a user types into --target or `import` matches
// the one the engine renders byte for byte.
package addr

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// Format renders a resource instance address. An empty key is a singular
// resource (`type.name`); otherwise the key is rendered as an HCL quoted string
// (`type.name["key"]`), escaped exactly as HCL expects so Parse round-trips it.
func Format(typ, name, key string) string {
	if key == "" {
		return typ + "." + name
	}
	return typ + "." + name + "[" + Quote(key) + "]"
}

// Quote renders s as an HCL quoted string literal (`"` and `\` escaped, and
// template sequences like `${` doubled so they stay literal).
func Quote(s string) string {
	return strings.TrimSpace(string(hclwrite.TokensForValue(cty.StringVal(s)).Bytes()))
}

// Parse parses `type.name` or `type.name["key"]`. hasKey reports whether an
// index was present (keys are never empty, so key != "" is equivalent).
func Parse(s string) (typ, name, key string, hasKey bool, err error) {
	tr, diags := hclsyntax.ParseTraversalAbs([]byte(s), "", hcl.InitialPos)
	if diags.HasErrors() {
		if i := strings.IndexByte(s, '['); i >= 0 && !strings.HasPrefix(strings.TrimSpace(s[i+1:]), `"`) {
			return "", "", "", false, fmt.Errorf("invalid address %q: key must be a quoted string", s)
		}
		return "", "", "", false, fmt.Errorf("invalid address %q: %s", s, diags[0].Summary)
	}
	if len(tr) < 2 || len(tr) > 3 {
		return "", "", "", false, fmt.Errorf(`invalid address %q: expected type.name or type.name["key"]`, s)
	}
	nameStep, ok := tr[1].(hcl.TraverseAttr)
	if !ok {
		return "", "", "", false, fmt.Errorf(`invalid address %q: expected type.name or type.name["key"]`, s)
	}
	typ, name = tr.RootName(), nameStep.Name
	if len(tr) == 2 {
		return typ, name, "", false, nil
	}
	idx, ok := tr[2].(hcl.TraverseIndex)
	if !ok {
		return "", "", "", false, fmt.Errorf(`invalid address %q: expected type.name or type.name["key"]`, s)
	}
	if idx.Key.Type() != cty.String {
		return "", "", "", false, fmt.Errorf("invalid address %q: key must be a quoted string", s)
	}
	key = idx.Key.AsString()
	if key == "" {
		return "", "", "", false, fmt.Errorf("invalid address %q: key must not be empty", s)
	}
	return typ, name, key, true, nil
}
