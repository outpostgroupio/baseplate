package plan

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// PathRef is a reference inside an attribute's value: an element of a list, a
// field of an object, or deeper (e.g. thumbnails[1].asset_id). The attribute
// stays in Attributes with an unknown at Path, which FillRefs replaces with the
// referenced value.
type PathRef struct {
	Path cty.Path
	provider.Ref
}

// FillRefs returns v with the value lookup gives for each ref set at its path,
// converted to the type of the value it replaces. A ref lookup doesn't resolve
// (ok false) is left as it is. v is not mutated.
func FillRefs(v cty.Value, refs []PathRef, lookup func(provider.Ref) (cty.Value, bool)) (cty.Value, error) {
	if len(refs) == 0 {
		return v, nil
	}
	return cty.Transform(v, func(p cty.Path, cur cty.Value) (cty.Value, error) {
		for _, r := range refs {
			if !p.Equals(r.Path) {
				continue
			}
			nv, ok := lookup(r.Ref)
			if !ok {
				return cur, nil
			}
			if cur.Type() == cty.DynamicPseudoType {
				return nv, nil
			}
			cv, err := convert.Convert(nv, cur.Type())
			if err != nil {
				return cty.NilVal, fmt.Errorf("%s.%s: %w", r.Address, r.Attr, err)
			}
			return cv, nil
		}
		return cur, nil
	})
}

// pathRefTargets reports whether any of refs points at an address in set.
func pathRefTargets(refs []PathRef, set map[string]bool) bool {
	for _, r := range refs {
		if set[r.Address] {
			return true
		}
	}
	return false
}
