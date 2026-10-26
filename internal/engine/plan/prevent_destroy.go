package plan

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/provider"
)

// CheckPreventDestroy returns an error for every delete or replace in the plan
// whose resource block sets lifecycle.prevent_destroy (see Plan.PreventDestroy).
// A replace destroys the current remote object (after creating its successor),
// so it is blocked too, as in Terraform. It runs on the final plan, after any
// --target narrowing, so a protected resource outside the targeted scope
// doesn't block the plan, as in Terraform.
//
// A resource whose remote object has vanished is re-created, not deleted, so
// it isn't guarded.
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
		if !protected[addr.Format(c.Type, c.Name, "")] {
			continue
		}
		switch c.Action {
		case provider.ActionDelete:
			errs = append(errs, fmt.Errorf("Instance cannot be destroyed: %s has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed. To avoid this error and continue with the plan, either disable lifecycle.prevent_destroy or reduce the scope of the plan using the --target flag.", c.Address))
		case provider.ActionReplace:
			fate := "destroys the current object"
			if c.AbandonsOld {
				fate = "leaves the current object unmanaged"
			}
			errs = append(errs, fmt.Errorf("Instance cannot be destroyed: %s has lifecycle.prevent_destroy set, but the plan calls for this resource to be replaced (a change to %s forces replacement, which %s). To avoid this error and continue with the plan, either disable lifecycle.prevent_destroy, revert the change, or reduce the scope of the plan using the --target flag.", c.Address, strings.Join(c.Replace, ", "), fate))
		}
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
