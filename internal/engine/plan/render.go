package plan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

var symbols = map[provider.ChangeAction]string{
	provider.ActionCreate: "+",
	provider.ActionUpdate: "~",
	provider.ActionDelete: "-",
	provider.ActionNoop:   " ",
}

type JSONAttr struct {
	Name string `json:"name"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
}
type JSONChange struct {
	Address  string     `json:"address"`
	Key      string     `json:"key,omitempty"`
	Action   string     `json:"action"`
	ImportID string     `json:"import_id,omitempty"`
	Attrs    []JSONAttr `json:"attributes,omitempty"`
}
type Summary struct {
	Import  int `json:"import"`
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
}

// JSONChanges renders every change as a machine-readable value. Sensitive
// attribute values are redacted. The result is never nil (empty plan -> []).
func (p *Plan) JSONChanges() []JSONChange {
	out := make([]JSONChange, 0, len(p.Changes))
	for _, c := range p.Changes {
		jc := JSONChange{Address: c.Address, Key: c.Key, Action: c.Action.String(), ImportID: c.ImportID}
		sensitive := map[string]bool{}
		for _, k := range c.Sensitive {
			sensitive[k] = true
		}
		for _, d := range c.AttrDiffs {
			if sensitive[d.Name] {
				jc.Attrs = append(jc.Attrs, JSONAttr{Name: d.Name, Old: "(sensitive value)", New: "(sensitive value)"})
				continue
			}
			jc.Attrs = append(jc.Attrs, JSONAttr{Name: d.Name, Old: ctyString(d.Old), New: ctyString(d.New)})
		}
		out = append(out, jc)
	}
	return out
}

// Warnings flags plan shapes that are legal but probably unintended. First, a
// for_each resource that declares zero instances while state still holds some
// (e.g. a fileset that matched nothing): every instance is destroyed. Second,
// a resource (type.name) that both destroys and creates instances in the
// same plan, which is what renaming a for_each key looks like. A created
// instance is a new remote object with a new id (for roblox_asset, a fresh
// upload), so it is worth a second look before applying. Instances are not
// paired up — with several of each there is no meaningful old→new mapping — so
// the warning names the resource and the counts. The result is nil when there
// is nothing to warn about; order is by resource address.
func (p *Plan) Warnings() []string {
	type counts struct{ del, add int }
	byRes := map[string]*counts{}
	for _, c := range p.Changes {
		if c.ImportID != "" || (c.Action != provider.ActionDelete && c.Action != provider.ActionCreate) {
			continue
		}
		base := addr.Format(c.Type, c.Name, "")
		n := byRes[base]
		if n == nil {
			n = &counts{}
			byRes[base] = n
		}
		if c.Action == provider.ActionDelete {
			n.del++
		} else {
			n.add++
		}
	}
	var bases []string
	for b, n := range byRes {
		if n.del > 0 && n.add > 0 {
			bases = append(bases, b)
		}
	}
	sort.Strings(bases)
	var out []string
	empty := append([]string(nil), p.EmptyForEach...)
	sort.Strings(empty)
	for _, b := range empty {
		if n := byRes[b]; n != nil && n.del > 0 {
			out = append(out, fmt.Sprintf("for_each for %s is empty; all %d instances will be destroyed.", b, n.del))
		}
	}
	for _, b := range bases {
		n := byRes[b]
		out = append(out, fmt.Sprintf(
			"%s: this plan destroys %d instance(s) and creates %d. Each created instance is a new remote object "+
				"with a new id (for roblox_asset, a new upload), so if a for_each key was renamed the object is "+
				"replaced, not moved. moved {} blocks, which will rename an instance in place, are planned.",
			b, n.del, n.add))
	}
	return out
}

// Summary counts changes by action, matching the RenderText footer.
func (p *Plan) Summary() Summary {
	var s Summary
	for _, c := range p.Changes {
		if c.ImportID != "" {
			s.Import++
			continue
		}
		switch c.Action {
		case provider.ActionCreate:
			s.Add++
		case provider.ActionUpdate:
			s.Change++
		case provider.ActionDelete:
			s.Destroy++
		}
	}
	return s
}

func (p *Plan) RenderText(color bool) string {
	var b strings.Builder
	var add, chg, del, imp int
	for _, c := range p.Changes {
		if c.ImportID != "" {
			imp++
			fmt.Fprintf(&b, "%s will be imported (id %s)\n", c.Address, c.ImportID)
			sensitive := map[string]bool{}
			for _, k := range c.Sensitive {
				sensitive[k] = true
			}
			for _, d := range c.AttrDiffs {
				if sensitive[d.Name] {
					fmt.Fprintf(&b, "    %s: (sensitive value) => (sensitive value)\n", d.Name)
					continue
				}
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
		sensitive := map[string]bool{}
		for _, k := range c.Sensitive {
			sensitive[k] = true
		}
		for _, k := range keys {
			if sensitive[k] {
				fmt.Fprintf(&b, "    %s = (sensitive value)\n", k)
				continue
			}
			if v, ok := attrs[k]; ok {
				fmt.Fprintf(&b, "    %s = %s\n", k, ctyString(v))
			} else {
				// referenced attribute — value not known until apply
				fmt.Fprintf(&b, "    %s = (known after apply)\n", k)
			}
		}
	}
	for _, w := range p.Warnings() {
		fmt.Fprintf(&b, "\nWarning: %s\n", w)
	}
	fmt.Fprintf(&b, "\nPlan: %d to import, %d to add, %d to change, %d to destroy.\n", imp, add, chg, del)
	return b.String()
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
		if t := v.Type(); t.IsListType() || t.IsSetType() || t.IsTupleType() {
			// Render collections HCL-style: ["a", "b"] (strings quoted).
			parts := []string{}
			for it := v.ElementIterator(); it.Next(); {
				_, e := it.Element()
				if e.IsKnown() && !e.IsNull() && e.Type() == cty.String {
					parts = append(parts, strconv.Quote(e.AsString()))
				} else {
					parts = append(parts, ctyString(e))
				}
			}
			return "[" + strings.Join(parts, ", ") + "]"
		}
		return v.GoString()
	}
}
