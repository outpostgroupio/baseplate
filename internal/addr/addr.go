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
	if plain(key) {
		return typ + "." + name + `["` + key + `"]`
	}
	return typ + "." + name + "[" + hclQuote(key) + "]"
}

// Quote renders s as an HCL quoted string literal (`"` and `\` escaped, and
// template sequences like `${` doubled so they stay literal).
func Quote(s string) string {
	if plain(s) {
		return `"` + s + `"`
	}
	return hclQuote(s)
}

// Matches reports whether address is the canonical address of type/name/key,
// without allocating for keys that need no escaping. State lookups run this
// once per resource, so it has to stay cheap with thousands of instances.
func Matches(typ, name, key, address string) bool {
	n := len(typ)
	if len(address) <= n || address[:n] != typ || address[n] != '.' {
		return false
	}
	rest := address[n+1:]
	if len(rest) < len(name) || rest[:len(name)] != name {
		return false
	}
	rest = rest[len(name):]
	if key == "" {
		return rest == ""
	}
	if plain(key) {
		return len(rest) == len(key)+4 && rest[:2] == `["` && rest[2:2+len(key)] == key && rest[2+len(key):] == `"]`
	}
	return rest == "["+hclQuote(key)+"]"
}

// plain reports whether s renders as an HCL string literal unchanged: printable
// ASCII with no quote, backslash, or template introducer ($ or %).
func plain(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' || c == '$' || c == '%' {
			return false
		}
	}
	return true
}

// hclQuote is the canonical (slow) quoting via the HCL writer.
func hclQuote(s string) string {
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
