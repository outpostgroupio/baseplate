package plan

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
)

func stateWith(rs ...state.Resource) *state.State {
	s := state.New()
	for _, r := range rs {
		s.Upsert(r)
	}
	return s
}

func addresses(s *state.State) []string {
	var out []string
	for _, r := range s.Resources {
		out = append(out, r.Address()+"="+r.ID)
	}
	sort.Strings(out)
	return out
}

func TestApplyMovesInstanceRename(t *testing.T) {
	s := stateWith(
		state.Resource{Type: "mock_thing", Name: "set", Key: "ui/old.png", ID: "1"},
		state.Resource{Type: "mock_thing", Name: "set", Key: "keep", ID: "2"},
	)
	moves, err := ApplyMoves(s, []MovedBlock{{From: `mock_thing.set["ui/old.png"]`, To: `mock_thing.set["ui/new.png"]`}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Move{{From: `mock_thing.set["ui/old.png"]`, To: `mock_thing.set["ui/new.png"]`}}
	if !reflect.DeepEqual(moves, want) {
		t.Fatalf("moves = %v, want %v", moves, want)
	}
	if got := addresses(s); !reflect.DeepEqual(got, []string{`mock_thing.set["keep"]=2`, `mock_thing.set["ui/new.png"]=1`}) {
		t.Fatalf("state = %v", got)
	}
}

func TestApplyMovesSingularToKeyedAndBack(t *testing.T) {
	s := stateWith(state.Resource{Type: "mock_thing", Name: "a", ID: "1"})
	if _, err := ApplyMoves(s, []MovedBlock{{From: "mock_thing.a", To: `mock_thing.a["k"]`}}); err != nil {
		t.Fatal(err)
	}
	if got := addresses(s); !reflect.DeepEqual(got, []string{`mock_thing.a["k"]=1`}) {
		t.Fatalf("singular -> keyed: state = %v", got)
	}
	if _, err := ApplyMoves(s, []MovedBlock{{From: `mock_thing.a["k"]`, To: "mock_thing.a"}}); err != nil {
		t.Fatal(err)
	}
	if got := addresses(s); !reflect.DeepEqual(got, []string{"mock_thing.a=1"}) {
		t.Fatalf("keyed -> singular: state = %v", got)
	}
}

func TestApplyMovesWholeResourceRenameKeepsKeys(t *testing.T) {
	s := stateWith(
		state.Resource{Type: "mock_thing", Name: "old", Key: "a", ID: "1"},
		state.Resource{Type: "mock_thing", Name: "old", Key: "b", ID: "2"},
		state.Resource{Type: "mock_thing", Name: "other", ID: "3"},
	)
	moves, err := ApplyMoves(s, []MovedBlock{{From: "mock_thing.old", To: "mock_thing.new"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 2 || moves[0].To != `mock_thing.new["a"]` || moves[1].To != `mock_thing.new["b"]` {
		t.Fatalf("moves = %v", moves)
	}
	if got := addresses(s); !reflect.DeepEqual(got, []string{`mock_thing.new["a"]=1`, `mock_thing.new["b"]=2`, "mock_thing.other=3"}) {
		t.Fatalf("state = %v", got)
	}
}

func TestApplyMovesIdempotentAndNoops(t *testing.T) {
	s := stateWith(state.Resource{Type: "mock_thing", Name: "new", ID: "1"})
	// to in state, from not: already done.
	moves, err := ApplyMoves(s, []MovedBlock{
		{From: "mock_thing.old", To: "mock_thing.new"},
		// neither in state: nothing to do (as in Terraform).
		{From: `mock_thing.x["a"]`, To: `mock_thing.x["b"]`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 0 {
		t.Fatalf("want no moves, got %v", moves)
	}
	if got := addresses(s); !reflect.DeepEqual(got, []string{"mock_thing.new=1"}) {
		t.Fatalf("state = %v", got)
	}
}

func TestApplyMovesBothInStateErrors(t *testing.T) {
	for name, block := range map[string]MovedBlock{
		"instance": {From: `mock_thing.set["a"]`, To: `mock_thing.set["b"]`},
		"resource": {From: "mock_thing.set", To: "mock_thing.dst"},
	} {
		s := stateWith(
			state.Resource{Type: "mock_thing", Name: "set", Key: "a", ID: "1"},
			state.Resource{Type: "mock_thing", Name: "set", Key: "b", ID: "2"},
			state.Resource{Type: "mock_thing", Name: "dst", Key: "z", ID: "3"},
		)
		_, err := ApplyMoves(s, []MovedBlock{block})
		if err == nil || !strings.Contains(err.Error(), "are in state") {
			t.Fatalf("%s: want a both-in-state error, got %v", name, err)
		}
	}
}

func TestApplyMovesTypeChangeErrors(t *testing.T) {
	_, err := ApplyMoves(state.New(), []MovedBlock{{From: "mock_thing.a", To: "mock_child.a"}})
	if err == nil || !strings.Contains(err.Error(), "type differs") {
		t.Fatalf("want a type-change error, got %v", err)
	}
}

func TestApplyMovesChainResolvesInAnyDeclarationOrder(t *testing.T) {
	s := stateWith(state.Resource{Type: "mock_thing", Name: "a", ID: "1"})
	// Declared out of order: b -> c first, then a -> b.
	moves, err := ApplyMoves(s, []MovedBlock{
		{From: "mock_thing.b", To: `mock_thing.c["k"]`},
		{From: "mock_thing.a", To: "mock_thing.b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := addresses(s); !reflect.DeepEqual(got, []string{`mock_thing.c["k"]=1`}) {
		t.Fatalf("state = %v", got)
	}
	// A chain is reported as one move.
	if want := []Move{{From: "mock_thing.a", To: `mock_thing.c["k"]`}}; !reflect.DeepEqual(moves, want) {
		t.Fatalf("moves = %v, want %v", moves, want)
	}
	// Half-applied chain (state already at b): only the tail runs.
	s = stateWith(state.Resource{Type: "mock_thing", Name: "b", ID: "1"})
	if _, err := ApplyMoves(s, []MovedBlock{
		{From: "mock_thing.a", To: "mock_thing.b"},
		{From: "mock_thing.b", To: `mock_thing.c["k"]`},
	}); err != nil {
		t.Fatal(err)
	}
	if got := addresses(s); !reflect.DeepEqual(got, []string{`mock_thing.c["k"]=1`}) {
		t.Fatalf("state = %v", got)
	}
}

func TestApplyMovesCycleErrors(t *testing.T) {
	s := stateWith(state.Resource{Type: "mock_thing", Name: "a", ID: "1"})
	_, err := ApplyMoves(s, []MovedBlock{
		{From: "mock_thing.a", To: "mock_thing.b"},
		{From: "mock_thing.b", To: "mock_thing.a"},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want a cycle error, got %v", err)
	}
}

func TestApplyMovesRewritesDependencies(t *testing.T) {
	s := stateWith(
		state.Resource{Type: "mock_thing", Name: "set", Key: "old", ID: "1", Dependencies: []string{"mock_thing.base"}},
		state.Resource{Type: "mock_thing", Name: "base", ID: "2"},
		state.Resource{Type: "mock_child", Name: "c", ID: "3", Dependencies: []string{`mock_thing.set["old"]`, "mock_thing.base"}},
	)
	if _, err := ApplyMoves(s, []MovedBlock{
		{From: `mock_thing.set["old"]`, To: `mock_thing.set["new"]`},
		{From: "mock_thing.base", To: "mock_thing.root"},
	}); err != nil {
		t.Fatal(err)
	}
	moved, _ := s.Find(`mock_thing.set["new"]`)
	if !reflect.DeepEqual(moved.Dependencies, []string{"mock_thing.root"}) {
		t.Fatalf("moved resource's own dependencies = %v", moved.Dependencies)
	}
	child, _ := s.Find("mock_child.c")
	if !reflect.DeepEqual(child.Dependencies, []string{`mock_thing.set["new"]`, "mock_thing.root"}) {
		t.Fatalf("dependent's dependencies = %v", child.Dependencies)
	}
}

// A moved resource plans as a no-op at its new address: no delete, no create.
func TestMovedThenBuildIsNoop(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("gem")})
	s := stateWith(state.Resource{Type: "mock_thing", Name: "set", Key: "ui/gem.png", ID: "mock_thing/1"})
	moves, err := ApplyMoves(s, []MovedBlock{{From: `mock_thing.set["ui/gem.png"]`, To: `mock_thing.set["icons/gem.png"]`}})
	if err != nil {
		t.Fatal(err)
	}
	pl, err := Build(context.Background(), p, s, []DesiredResource{keyed("icons/gem.png", "gem")})
	if err != nil {
		t.Fatal(err)
	}
	pl.Moves = moves
	if len(pl.Changes) != 1 || pl.Changes[0].Action != provider.ActionNoop {
		t.Fatalf("want one no-op change, got %+v", pl.Changes)
	}
	if !pl.HasChanges() {
		t.Fatal("a move is a change (exit code 2)")
	}
	if ws := pl.Warnings(); ws != nil {
		t.Fatalf("no rename warning once moved: %v", ws)
	}
	if sum := pl.Summary(); sum.Move != 1 || sum.Add != 0 || sum.Destroy != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	out := pl.RenderText(false)
	for _, want := range []string{
		`~ mock_thing.set["ui/gem.png"] has moved to mock_thing.set["icons/gem.png"]`,
		"Plan: 0 to import, 0 to add, 0 to change, 0 to destroy, 1 to move.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("text plan missing %q:\n%s", want, out)
		}
	}
	if js := pl.JSONMoves(); len(js) != 1 || js[0].From != `mock_thing.set["ui/gem.png"]` || js[0].To != `mock_thing.set["icons/gem.png"]` {
		t.Fatalf("JSON moves = %+v", js)
	}
}

func TestRenameWarningGenericExampleForSeveralKeys(t *testing.T) {
	p := mock.New()
	s := state.New()
	for _, k := range []string{"a", "b"} {
		p.Seed("mock_thing/"+k, provider.Attributes{"name": cty.StringVal(k)})
		s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: k, ID: "mock_thing/" + k})
	}
	pl, err := Build(context.Background(), p, s, []DesiredResource{keyed("c", "c"), keyed("d", "d")})
	if err != nil {
		t.Fatal(err)
	}
	ws := pl.Warnings()
	if len(ws) != 1 || !strings.Contains(ws[0], `moved { from = mock_thing.set["old-key"]  to = mock_thing.set["new-key"] }`) {
		t.Fatalf("want the generic moved example, got %v", ws)
	}
}
