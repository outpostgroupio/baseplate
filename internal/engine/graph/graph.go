package graph

import (
	"fmt"
	"sort"
)

type Node struct {
	Address   string
	DependsOn []string
}

func TopoSort(nodes []Node) ([]string, error) {
	deps := map[string]map[string]bool{}
	for _, n := range nodes {
		if deps[n.Address] == nil {
			deps[n.Address] = map[string]bool{}
		}
		for _, d := range n.DependsOn {
			deps[n.Address][d] = true
		}
	}

	// A dependency that names an address with no corresponding node is a
	// dangling reference (e.g. a typo'd depends_on), not a cycle. Detect it up
	// front so the error is actionable instead of a misleading "cycle".
	for _, n := range nodes {
		for _, d := range n.DependsOn {
			if _, ok := deps[d]; !ok {
				return nil, fmt.Errorf("%s depends on unknown resource %s", n.Address, d)
			}
		}
	}

	var order []string
	done := map[string]bool{}
	for len(done) < len(deps) {
		// collect ready nodes (all deps done), pick lexically smallest for determinism.
		var ready []string
		for addr, ds := range deps {
			if done[addr] {
				continue
			}
			ok := true
			for d := range ds {
				if !done[d] {
					ok = false
					break
				}
			}
			if ok {
				ready = append(ready, addr)
			}
		}
		if len(ready) == 0 {
			return nil, fmt.Errorf("dependency cycle detected among %d unresolved resources", len(deps)-len(done))
		}
		sort.Strings(ready)
		for _, addr := range ready {
			order = append(order, addr)
			done[addr] = true
		}
	}
	return order, nil
}

func Reverse(order []string) []string {
	out := make([]string, len(order))
	for i, a := range order {
		out[len(order)-1-i] = a
	}
	return out
}
