// Package resourcedocs generates docs/resource-reference.md from each resource's
// schema (DecodeSpec/Computed/Sensitive) and its authored provider.Documented Doc().
package resourcedocs

import (
	"strings"

	"github.com/zclconf/go-cty/cty"
)

// attrTypeName renders a cty.Type as the short name used in the reference table.
func attrTypeName(t cty.Type) string {
	switch {
	case t == cty.String:
		return "string"
	case t == cty.Bool:
		return "bool"
	case t == cty.Number:
		return "number"
	default:
		return t.FriendlyName()
	}
}

// attrKind renders the Kind column for one attribute. createOnly means the
// attribute is in the resource's Computed() set (excluded from diffing);
// sensitive means it is in Sensitive().
func attrKind(required, createOnly, sensitive bool) string {
	parts := make([]string, 0, 3)
	if required {
		parts = append(parts, "required")
	} else {
		parts = append(parts, "optional")
	}
	if createOnly {
		parts = append(parts, "create-only")
	} else {
		parts = append(parts, "diffable")
	}
	if sensitive {
		parts = append(parts, "sensitive")
	}
	return strings.Join(parts, ", ")
}
