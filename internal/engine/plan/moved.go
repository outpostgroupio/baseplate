package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/outpostgroupio/baseplate/internal/addr"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// MovedBlock is one expanded `moved {}` block: the state entry recorded at From
// now lives at To. Both are canonical addresses (addr.Format).
//
// When neither address has an instance key, the block renames a whole resource:
// every instance of From (singular or keyed) moves to To, keeping its key.
// Otherwise it moves exactly one instance, and an unkeyed side means the
// singular instance; that is how a resource adopts for_each (`a.b` ->
// `a.b["k"]`) or drops it (`a.b["k"]` -> `a.b`). This matches Terraform.
type MovedBlock struct {
	From string
	To   string
}

// Move is one state entry a moved block relocated, as reported by plan. A
// chain of blocks (a -> b, b -> c) is reported as one move (a -> c).
type Move struct {
	From string
	To   string
}

// moveEnd is a parsed MovedBlock address. whole is set on both ends of a
// whole-resource rename.
type moveEnd struct {
	typ, name, key string
	whole          bool
}

func (e moveEnd) String() string {
	return addr.Format(e.typ, e.name, e.key)
}

// overlaps reports whether a move that ends at e feeds a move that starts at o,
// i.e. whether they can address the same state entry.
func (e moveEnd) overlaps(o moveEnd) bool {
	return e.typ == o.typ && e.name == o.name && (e.whole || o.whole || e.key == o.key)
}

// ApplyMoves applies moved blocks to s in memory, before planning, and returns
// the moves it made. It is a state-only rename: no provider is called and ids
// are kept, so a moved resource plans as a no-op (or an update) at its new
// address instead of a delete plus a create. The caller decides whether to
// persist the result: plan never does, apply does (see Apply).
//
// For each block, in chain order:
//   - From in state and To not: move it.
//   - To in state and From not: already moved (idempotent), nothing to do.
//   - Neither in state: nothing to do, as in Terraform (e.g. the resource
//     was never created, or the block is older than the state).
//   - Both in state: an error. Terraform skips such a move with a warning;
//     Baseplate refuses, because silently leaving two objects where the
//     config expects one is how an asset gets re-uploaded.
//
// Blocks are ordered so a chain (a -> b, b -> c) resolves whatever order it is
// declared in; a cycle (a -> b, b -> a) is an error. Every recorded
// Dependencies entry naming a moved address is rewritten to the new address.
func ApplyMoves(s *state.State, blocks []MovedBlock) ([]Move, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	froms := make([]moveEnd, len(blocks))
	tos := make([]moveEnd, len(blocks))
	for i, b := range blocks {
		ft, fn, fk, fKeyed, err := addr.Parse(b.From)
		if err != nil {
			return nil, fmt.Errorf("moved block: from: %w", err)
		}
		tt, tn, tk, tKeyed, err := addr.Parse(b.To)
		if err != nil {
			return nil, fmt.Errorf("moved block: to: %w", err)
		}
		if ft != tt {
			return nil, fmt.Errorf("moved block from %s to %s: the resource type differs (%s vs %s); a move can only rename a resource, not change its type", b.From, b.To, ft, tt)
		}
		if b.From == b.To {
			return nil, fmt.Errorf("moved block from %s to %s: from and to are the same address", b.From, b.To)
		}
		whole := !fKeyed && !tKeyed
		froms[i] = moveEnd{typ: ft, name: fn, key: fk, whole: whole}
		tos[i] = moveEnd{typ: tt, name: tn, key: tk, whole: whole}
	}
	order, err := moveOrder(blocks, froms, tos)
	if err != nil {
		return nil, err
	}

	// origin maps a moved entry's current address to where it started, so a
	// chain is reported as one move.
	origin := map[string]string{}
	var seq []string // current addresses, in the order they were first moved
	for _, i := range order {
		from, to := froms[i], tos[i]
		var src []int
		dstInState := ""
		for j := range s.Resources {
			r := &s.Resources[j]
			if r.Type != from.typ {
				continue
			}
			if from.whole {
				if r.Name == from.name {
					src = append(src, j)
				}
				if r.Name == to.name && dstInState == "" {
					dstInState = r.Address()
				}
				continue
			}
			if r.Name == from.name && r.Key == from.key {
				src = append(src, j)
			}
			if r.Name == to.name && r.Key == to.key {
				dstInState = r.Address()
			}
		}
		if len(src) == 0 {
			continue // already moved, or never created: nothing to do
		}
		if dstInState != "" {
			return nil, fmt.Errorf("moved block from %s to %s: both %s and %s are in state, so Baseplate can't tell which object should end up at %s. "+
				"If the move already happened and the old entry is stale, remove the moved block; otherwise destroy or rename one of them first",
				blocks[i].From, blocks[i].To, s.Resources[src[0]].Address(), dstInState, blocks[i].To)
		}
		renamed := map[string]string{}
		for _, j := range src {
			r := &s.Resources[j]
			old := r.Address()
			r.Name = to.name
			if !from.whole {
				r.Key = to.key
			}
			nu := r.Address()
			renamed[old] = nu
			if o, ok := origin[old]; ok {
				delete(origin, old)
				origin[nu] = o
				for k, cur := range seq {
					if cur == old {
						seq[k] = nu
					}
				}
			} else {
				origin[nu] = old
				seq = append(seq, nu)
			}
		}
		for j := range s.Resources {
			deps := s.Resources[j].Dependencies
			for k, d := range deps {
				if nu, ok := renamed[d]; ok {
					deps[k] = nu
				}
			}
		}
	}
	var out []Move
	for _, cur := range seq {
		if o := origin[cur]; o != cur {
			out = append(out, Move{From: o, To: cur})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].From < out[b].From })
	return out, nil
}

// moveOrder returns the block indexes so that a block whose To feeds another
// block's From runs first (a -> b before b -> c). Ties keep declaration order.
func moveOrder(blocks []MovedBlock, froms, tos []moveEnd) ([]int, error) {
	n := len(blocks)
	after := make([][]int, n) // after[i]: blocks that must run after i
	indeg := make([]int, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && tos[i].overlaps(froms[j]) {
				after[i] = append(after[i], j)
				indeg[j]++
			}
		}
	}
	done := make([]bool, n)
	order := make([]int, 0, n)
	for len(order) < n {
		next := -1
		for i := 0; i < n; i++ {
			if !done[i] && indeg[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			var cyc []string
			for i := 0; i < n; i++ {
				if !done[i] {
					cyc = append(cyc, blocks[i].From+" -> "+blocks[i].To)
				}
			}
			return nil, fmt.Errorf("moved blocks form a cycle: %s; a chain of moves must end somewhere", strings.Join(cyc, ", "))
		}
		done[next] = true
		order = append(order, next)
		for _, j := range after[next] {
			indeg[j]--
		}
	}
	return order, nil
}
