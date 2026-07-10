package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
)

var symbols = map[provider.ChangeAction]string{
	provider.ActionCreate: "+",
	provider.ActionUpdate: "~",
	provider.ActionDelete: "-",
	provider.ActionNoop:   " ",
}

func (p *Plan) RenderText(color bool) string {
	var b strings.Builder
	var add, chg, del, imp int
	for _, c := range p.Changes {
		if c.ImportID != "" {
			imp++
			fmt.Fprintf(&b, "%s will be imported (id %s)\n", c.Address, c.ImportID)
			for _, d := range c.AttrDiffs {
				fmt.Fprintf(&b, "    %s: %s => %s\n", d.Name, ctyString(d.Old), ctyString(d.New))
			}
			continue
		}
		if c.Action == provider.ActionNoop {
			continue
		}
		switch c.Action {
		case provider.ActionCreate:
			add++
		case provider.ActionUpdate:
			chg++
		case provider.ActionDelete:
			del++
		}
		fmt.Fprintf(&b, "%s %s (%s)\n", symbols[c.Action], c.Address, c.Action)
		attrs := c.Desired
		if c.Action == provider.ActionDelete {
			attrs = c.Actual
		}
		keyset := map[string]bool{}
		for k := range attrs {
			keyset[k] = true
		}
		if c.Action != provider.ActionDelete {
			for k := range c.Refs {
				keyset[k] = true
			}
		}
		var keys []string
		for k := range keyset {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v, ok := attrs[k]; ok {
				fmt.Fprintf(&b, "    %s = %s\n", k, ctyString(v))
			} else {
				// referenced attribute — value not known until apply
				fmt.Fprintf(&b, "    %s = (known after apply)\n", k)
			}
		}
	}
	fmt.Fprintf(&b, "\nPlan: %d to import, %d to add, %d to change, %d to destroy.\n", imp, add, chg, del)
	return b.String()
}

func (p *Plan) RenderJSON() ([]byte, error) {
	type jsonAttr struct {
		Name string `json:"name"`
		Old  string `json:"old,omitempty"`
		New  string `json:"new,omitempty"`
	}
	type jsonChange struct {
		Address  string     `json:"address"`
		Action   string     `json:"action"`
		ImportID string     `json:"import_id,omitempty"`
		Attrs    []jsonAttr `json:"attributes,omitempty"`
	}
	var out []jsonChange
	for _, c := range p.Changes {
		jc := jsonChange{Address: c.Address, Action: c.Action.String(), ImportID: c.ImportID}
		for _, d := range c.AttrDiffs {
			jc.Attrs = append(jc.Attrs, jsonAttr{Name: d.Name, Old: ctyString(d.Old), New: ctyString(d.New)})
		}
		out = append(out, jc)
	}
	return json.MarshalIndent(out, "", "  ")
}

func ctyString(v cty.Value) string {
	if !v.IsKnown() {
		return "(known after apply)"
	}
	if v.IsNull() {
		return "null"
	}
	switch v.Type() {
	case cty.String:
		return v.AsString()
	case cty.Bool:
		if v.True() {
			return "true"
		}
		return "false"
	case cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			i, _ := bf.Int64()
			return strconv.FormatInt(i, 10)
		}
		return bf.Text('f', -1)
	default:
		return v.GoString()
	}
}
