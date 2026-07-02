package plan

import (
	"encoding/json"
	"fmt"
	"sort"
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
	var add, chg, del int
	for _, c := range p.Changes {
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
		var keys []string
		for k := range attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "    %s = %s\n", k, ctyString(attrs[k]))
		}
	}
	fmt.Fprintf(&b, "\nPlan: %d to add, %d to change, %d to destroy.\n", add, chg, del)
	return b.String()
}

func (p *Plan) RenderJSON() ([]byte, error) {
	type jsonAttr struct {
		Name string `json:"name"`
		Old  string `json:"old,omitempty"`
		New  string `json:"new,omitempty"`
	}
	type jsonChange struct {
		Address string     `json:"address"`
		Action  string     `json:"action"`
		Attrs   []jsonAttr `json:"attributes,omitempty"`
	}
	var out []jsonChange
	for _, c := range p.Changes {
		jc := jsonChange{Address: c.Address, Action: c.Action.String()}
		for _, d := range c.AttrDiffs {
			jc.Attrs = append(jc.Attrs, jsonAttr{Name: d.Name, Old: ctyString(d.Old), New: ctyString(d.New)})
		}
		out = append(out, jc)
	}
	return json.MarshalIndent(out, "", "  ")
}

func ctyString(v cty.Value) string {
	if v.IsNull() {
		return "null"
	}
	if v.Type() == cty.String {
		return v.AsString()
	}
	return v.GoString()
}
