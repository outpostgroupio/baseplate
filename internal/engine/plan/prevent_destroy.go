package plan

import (
	"errors"
	"fmt"
	"sort"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

// CheckPreventDestroy returns an error for every delete in the plan whose
// resource block sets lifecycle.prevent_destroy (see Plan.PreventDestroy). It
// runs on the final plan, after any --target narrowing, so a protected
// resource outside the targeted scope doesn't block the plan, as in Terraform.
//
// The engine has no replace (destroy-then-create) action: a resource whose
// remote object has vanished is re-created, not deleted, so deletes are the
// only destroy path to guard.
func (p *Plan) CheckPreventDestroy() error {
	if len(p.PreventDestroy) == 0 {
		return nil
	}
	protected := map[string]bool{}
	for _, b := range p.PreventDestroy {
		protected[b] = true
	}
	var errs []error
	for _, c := range p.Changes {
		if c.Action != provider.ActionDelete || !protected[addr.Format(c.Type, c.Name, "")] {
			continue
		}
		errs = append(errs, fmt.Errorf("Instance cannot be destroyed: %s has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed. To avoid this error and continue with the plan, either disable lifecycle.prevent_destroy or reduce the scope of the plan using the --target flag.", c.Address))
	}
	return errors.Join(errs...)
}

// protectedBlocks returns the sorted, de-duplicated type.name of every desired
// resource with PreventDestroy set.
func protectedBlocks(desired []DesiredResource) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range desired {
		base := addr.Format(d.Type, d.Name, "")
		if d.PreventDestroy && !seen[base] {
			seen[base] = true
			out = append(out, base)
		}
	}
	sort.Strings(out)
	return out
}
