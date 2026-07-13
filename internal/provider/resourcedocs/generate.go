package resourcedocs

//go:generate go run ./cmd/gen-resource-docs -out ../../../docs/resource-reference.md

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hcldec"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

const preamble = "# Resource reference\n\n" +
	"The resources Baseplate can manage, and their attributes.\n\n" +
	"> **Generated file** — do not edit by hand. Run `go generate ./...` to regenerate from each\n" +
	"> resource's schema and `Doc()`.\n\n" +
	"**Legend for the _Kind_ column:**\n\n" +
	"- **required** / **optional** — whether the attribute must be set in config.\n" +
	"- **create-only** — set at create; changing it in config does **not** produce an update (it's\n" +
	"  excluded from the diff). To change it, taint/recreate the resource.\n" +
	"- **diffable** — a normal managed attribute; a config change plans an update.\n" +
	"- **sensitive** — redacted from all plan output and never written to state.\n\n" +
	"Every resource also has an implicit `id` (Baseplate's state id) — you never set it in config.\n"

// Generate renders the full resource-reference markdown for the provider's
// resources, sorted by Type(). It errors if a resource does not implement
// provider.Documented, if a DecodeSpec attribute has no Doc().Attributes entry,
// or if Doc().Attributes has a key that is not a DecodeSpec attribute.
func Generate(p provider.Provider) (string, error) {
	types := make([]string, 0, len(p.Resources()))
	for t := range p.Resources() {
		types = append(types, t)
	}
	sort.Strings(types)

	var b strings.Builder
	b.WriteString(preamble)
	for _, t := range types {
		r := p.Resources()[t]
		section, err := renderResource(r)
		if err != nil {
			return "", fmt.Errorf("%s: %w", t, err)
		}
		b.WriteString("\n---\n\n")
		b.WriteString(section)
	}
	return b.String(), nil
}

func renderResource(r provider.Resource) (string, error) {
	doc, ok := r.(provider.Documented)
	if !ok {
		return "", fmt.Errorf("does not implement provider.Documented")
	}
	rd := doc.Doc()

	spec, ok := r.DecodeSpec().(hcldec.ObjectSpec)
	if !ok {
		return "", fmt.Errorf("DecodeSpec is not an hcldec.ObjectSpec")
	}
	computed := map[string]bool{}
	for _, c := range r.Computed() {
		computed[c] = true
	}
	sensitive := map[string]bool{}
	if s, ok := r.(provider.Sensitive); ok {
		for _, name := range s.Sensitive() {
			sensitive[name] = true
		}
	}

	// Collect + sort attribute names from the schema.
	names := make([]string, 0, len(spec))
	for name := range spec {
		names = append(names, name)
	}
	sort.Strings(names)

	// Error on any schema attribute without a description.
	for _, name := range names {
		if _, ok := rd.Attributes[name]; !ok {
			return "", fmt.Errorf("attribute %q has no Doc().Attributes entry", name)
		}
	}
	// Error on any doc key that is not a schema attribute.
	for k := range rd.Attributes {
		if _, ok := spec[k]; !ok {
			return "", fmt.Errorf("Doc().Attributes has key %q which is not a DecodeSpec attribute", k)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## `%s`\n\n", r.Type())
	if rd.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", rd.Summary)
	}
	b.WriteString("| Attribute | Type | Kind | Description |\n|---|---|---|---|\n")
	for _, name := range names {
		as, ok := spec[name].(*hcldec.AttrSpec)
		if !ok {
			return "", fmt.Errorf("attribute %q is not an *hcldec.AttrSpec", name)
		}
		kind := attrKind(as.Required, computed[name], sensitive[name])
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", name, attrTypeName(as.Type), kind, rd.Attributes[name])
	}
	if rd.Notes != "" {
		fmt.Fprintf(&b, "\n%s\n", rd.Notes)
	}
	if len(rd.Scopes) > 0 {
		quoted := make([]string, len(rd.Scopes))
		for i, s := range rd.Scopes {
			quoted[i] = "`" + s + "`"
		}
		fmt.Fprintf(&b, "\nRequires the %s [scope](how-to/api-key-scopes.md).\n", strings.Join(quoted, ", "))
	}
	if rd.ExampleFile != "" {
		fmt.Fprintf(&b, "\nExample: [`%s`](../%s).\n", rd.ExampleFile, rd.ExampleFile)
	}
	return b.String(), nil
}
