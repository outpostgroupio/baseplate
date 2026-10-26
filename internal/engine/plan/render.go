package plan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

var symbols = map[provider.ChangeAction]string{
	provider.ActionCreate:  "+",
	provider.ActionUpdate:  "~",
	provider.ActionDelete:  "-",
	provider.ActionReplace: "-/+",
	provider.ActionNoop:    " ",
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
	// ForcesReplacement names the attributes/triggers that force a replace
	// (action "replace").
	ForcesReplacement []string `json:"forces_replacement,omitempty"`
	// OldID is the id of the object a replace supersedes, and AbandonsOld is
	// true when that object is left in place (unmanaged) instead of destroyed.
	OldID       string `json:"old_id,omitempty"`
	AbandonsOld bool   `json:"abandons_old,omitempty"`
}

// JSONMove is one state entry a moved {} block relocated.
type JSONMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Summary counts a replace as one add and one destroy, as Terraform does, and
// also in Replace so consumers can tell replaced objects from plain ones.
type Summary struct {
	Move    int `json:"move"`
	Import  int `json:"import"`
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
	Replace int `json:"replace"`
}

// JSONChanges renders every change as a machine-readable value. Sensitive
// attribute values are redacted. The result is never nil (empty plan -> []).
func (p *Plan) JSONChanges() []JSONChange {
	out := make([]JSONChange, 0, len(p.Changes))
	for _, c := range p.Changes {
		jc := JSONChange{Address: c.Address, Key: c.Key, Action: c.Action.String(), ImportID: c.ImportID, ForcesReplacement: c.Replace}
		if c.Action == provider.ActionReplace {
			jc.OldID, jc.AbandonsOld = c.ID, c.AbandonsOld
		}
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

// JSONMoves renders the plan's moves; nil when there are none.
func (p *Plan) JSONMoves() []JSONMove {
	if len(p.Moves) == 0 {
		return nil
	}
	out := make([]JSONMove, len(p.Moves))
	for i, m := range p.Moves {
		out[i] = JSONMove{From: m.From, To: m.To}
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
	type counts struct {
		del, add     int
		delAt, addAt string // the first deleted / created instance
	}
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
			if n.del == 0 {
				n.delAt = c.Address
			}
			n.del++
		} else {
			if n.add == 0 {
				n.addAt = c.Address
			}
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
		// With exactly one of each, the pair is almost certainly the rename,
		// so the example names the real addresses; otherwise it can't pair
		// them up and shows the shape.
		from, to := b+`["old-key"]`, b+`["new-key"]`
		if n.del == 1 && n.add == 1 {
			from, to = n.delAt, n.addAt
		}
		out = append(out, fmt.Sprintf(
			"%s: this plan destroys %d instance(s) and creates %d. Each created instance is a new remote object "+
				"with a new id (for roblox_asset, a new upload), so if a for_each key was renamed the object is "+
				"replaced, not moved. To keep the existing object and its id, declare the rename with a moved block, "+
				"e.g. moved { from = %s  to = %s } (moved blocks also take for_each, to move many keys at once).",
			b, n.del, n.add, from, to))
	}
	for _, c := range p.Changes {
		if c.Warning != "" {
			out = append(out, c.Warning)
		}
	}
	return out
}

// Summary counts changes by action, matching the RenderText footer. An import
// counts only as an import, unless it is then replaced. A replace counts as an
// add, plus a destroy unless the old object is left in place (AbandonsOld).
func (p *Plan) Summary() Summary {
	s := Summary{Move: len(p.Moves)}
	for _, c := range p.Changes {
		if c.ImportID != "" {
			s.Import++
			if c.Action != provider.ActionReplace {
				continue
			}
		}
		switch c.Action {
		case provider.ActionCreate:
			s.Add++
		case provider.ActionUpdate:
			s.Change++
		case provider.ActionDelete:
			s.Destroy++
		case provider.ActionReplace:
			s.Add++
			s.Replace++
			if !c.AbandonsOld {
				s.Destroy++
			}
		}
	}
	return s
}

func (p *Plan) RenderText(color bool) string {
	var b strings.Builder
	for _, m := range p.Moves {
		fmt.Fprintf(&b, "~ %s has moved to %s\n", m.From, m.To)
	}
	for _, c := range p.Changes {
		forcing := map[string]bool{}
		for _, k := range c.Replace {
			forcing[k] = true
		}
		if c.ImportID != "" {
			then := ""
			if c.Action == provider.ActionReplace {
				then = ", then replaced"
			}
			fmt.Fprintf(&b, "%s will be imported (id %s)%s\n", c.Address, c.ImportID, then)
			sensitive := map[string]bool{}
			for _, k := range c.Sensitive {
				sensitive[k] = true
			}
			for _, d := range c.AttrDiffs {
				note := ""
				if forcing[d.Name] {
					note = forcesNote
				}
				if sensitive[d.Name] {
					fmt.Fprintf(&b, "    %s: (sensitive value) => (sensitive value)%s\n", d.Name, note)
					continue
				}
				fmt.Fprintf(&b, "    %s: %s => %s%s\n", d.Name, ctyString(d.Old), ctyString(d.New), note)
			}
			if c.Action == provider.ActionReplace {
				b.WriteString(oldObjectNote(c))
			}
			continue
		}
		if c.Action == provider.ActionNoop {
			continue
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
			if forcing[k] {
				continue // rendered as old => new below
			}
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
		// What forced the replace — an attribute or a trigger (e.g. a file's
		// content_hash) — shown old => new so the reason is visible.
		for _, d := range c.AttrDiffs {
			if !forcing[d.Name] {
				continue
			}
			if sensitive[d.Name] {
				fmt.Fprintf(&b, "    %s: (sensitive value) => (sensitive value)%s\n", d.Name, forcesNote)
				continue
			}
			fmt.Fprintf(&b, "    %s: %s => %s%s\n", d.Name, ctyString(d.Old), ctyString(d.New), forcesNote)
		}
		if c.Action == provider.ActionReplace {
			b.WriteString(oldObjectNote(c))
		}
	}
	for _, w := range p.Warnings() {
		fmt.Fprintf(&b, "\nWarning: %s\n", w)
	}
	sum := p.Summary()
	fmt.Fprintf(&b, "\nPlan: %d to import, %d to add, %d to change, %d to destroy", sum.Import, sum.Add, sum.Change, sum.Destroy)
	if sum.Move > 0 {
		fmt.Fprintf(&b, ", %d to move", sum.Move)
	}
	b.WriteString(".\n")
	return b.String()
}

// oldObjectNote says what a replace does with the object it supersedes.
func oldObjectNote(c Change) string {
	if c.AbandonsOld {
		return fmt.Sprintf("    # old object (id %s) is left in place, no longer managed\n", c.ID)
	}
	return fmt.Sprintf("    # old object (id %s) is destroyed after the new one is created\n", c.ID)
}

// forcesNote marks the attribute/trigger lines that force a replace.
const forcesNote = "  # forces replacement"

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
		// Maps/objects (e.g. a config map): render as compact JSON, which reads
		// like the HCL the user wrote. A partially-unknown value can't be
		// marshaled; it is only "known after apply" anyway.
		if !v.IsWhollyKnown() {
			return "(known after apply)"
		}
		if b, err := ctyjson.Marshal(v, v.Type()); err == nil {
			return string(b)
		}
		return v.GoString()
	}
}
