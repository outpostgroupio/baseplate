package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/mock"
	"github.com/outpostgroupio/baseplate/internal/state"
)

func keyed(key, name string) DesiredResource {
	return DesiredResource{Type: "mock_thing", Name: "set", Key: key,
		Attributes: provider.Attributes{"name": cty.StringVal(name)}}
}

func TestBuildKeyedInstancesCarryKeyAndAddress(t *testing.T) {
	pl, err := Build(context.Background(), mock.New(), state.New(),
		[]DesiredResource{keyed("b", "B"), keyed("a", "A")})
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 2 {
		t.Fatalf("want 2 creates, got %+v", pl.Changes)
	}
	c := pl.Changes[0]
	if c.Address != `mock_thing.set["a"]` || c.Name != "set" || c.Key != "a" {
		t.Fatalf("first change = %+v (want lexical key order, Name/Key set)", c)
	}
	js := pl.JSONChanges()
	if js[0].Key != "a" || js[0].Address != `mock_thing.set["a"]` {
		t.Fatalf("JSON change should carry key: %+v", js[0])
	}
}

func TestBuildDeleteCarriesStateKey(t *testing.T) {
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "gone", ID: "mock_thing/1"})
	pl, err := Build(context.Background(), mock.New(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Changes) != 1 || pl.Changes[0].Key != "gone" || pl.Changes[0].Name != "set" ||
		pl.Changes[0].Address != `mock_thing.set["gone"]` {
		t.Fatalf("delete should carry name/key from state: %+v", pl.Changes)
	}
}

func TestBuildRejectsMixedSingularAndKeyed(t *testing.T) {
	_, err := Build(context.Background(), mock.New(), state.New(), []DesiredResource{
		keyed("a", "A"),
		{Type: "mock_thing", Name: "set", Attributes: provider.Attributes{"name": cty.StringVal("x")}},
	})
	if err == nil || !strings.Contains(err.Error(), "both with and without for_each") || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("expected a mixing error pointing at moved, got %v", err)
	}
}

func TestWarningsFlagKeyRename(t *testing.T) {
	p := mock.New()
	p.Seed("mock_thing/1", provider.Attributes{"name": cty.StringVal("gem")})
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "ui/gem.png", ID: "mock_thing/1"})

	pl, err := Build(context.Background(), p, s, []DesiredResource{keyed("icons/gem.png", "gem")})
	if err != nil {
		t.Fatal(err)
	}
	ws := pl.Warnings()
	if len(ws) != 1 {
		t.Fatalf("want one rename warning, got %v", ws)
	}
	for _, want := range []string{"mock_thing.set:", "destroys 1 instance(s) and creates 1", "new id", "moved {}"} {
		if !strings.Contains(ws[0], want) {
			t.Fatalf("warning missing %q:\n%s", want, ws[0])
		}
	}
	// The warning must not recommend a state surgery workaround: there is no
	// command to drop an entry from state, and importing the old id under the
	// new key would leave two addresses managing one object.
	if strings.Contains(ws[0], "baseplate import") || strings.Contains(ws[0], "mock_thing/1") {
		t.Fatalf("warning must not suggest re-importing the old id:\n%s", ws[0])
	}
	if out := pl.RenderText(false); !strings.Contains(out, "Warning: mock_thing.set:") {
		t.Fatalf("text plan should print the warning:\n%s", out)
	}
}

func TestWarningsQuietForPlainAddOrDelete(t *testing.T) {
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "other", ID: "mock_thing/1"})
	pl, err := Build(context.Background(), mock.New(), s, []DesiredResource{keyed("a", "A")})
	if err != nil {
		t.Fatal(err)
	}
	if ws := pl.Warnings(); ws != nil {
		t.Fatalf("a delete and a create of different resources is not a rename: %v", ws)
	}
}

// With several deletes and creates on one resource there is no meaningful
// old→new pairing; the warning names the resource and the counts only.
func TestWarningsReportCountsWithoutPairing(t *testing.T) {
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "old1", ID: "mock_thing/1"})
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "old2", ID: "mock_thing/2"})
	pl, err := Build(context.Background(), mock.New(), s,
		[]DesiredResource{keyed("new1", "x"), keyed("new2", "y"), keyed("new3", "z")})
	if err != nil {
		t.Fatal(err)
	}
	ws := pl.Warnings()
	if len(ws) != 1 || !strings.Contains(ws[0], "mock_thing.set: this plan destroys 2 instance(s) and creates 3") {
		t.Fatalf("want one count-only warning, got %v", ws)
	}
	for _, addr := range []string{"old1", "old2", "new1", "new2", "new3"} {
		if strings.Contains(ws[0], addr) {
			t.Fatalf("warning must not pair specific instances (found %q):\n%s", addr, ws[0])
		}
	}
}

// A for_each that expands to nothing (e.g. a fileset that matched no files)
// destroys every instance in state; the plan must say so loudly.
func TestWarningsEmptyForEachDestroysAll(t *testing.T) {
	s := state.New()
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "a", ID: "mock_thing/1"})
	s.Upsert(state.Resource{Type: "mock_thing", Name: "set", Key: "b", ID: "mock_thing/2"})
	pl, err := Build(context.Background(), mock.New(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ws := pl.Warnings(); ws != nil {
		t.Fatalf("a removed resource block is not an empty for_each: %v", ws)
	}
	pl.EmptyForEach = []string{"mock_thing.set"}
	ws := pl.Warnings()
	if len(ws) != 1 || ws[0] != "for_each for mock_thing.set is empty; all 2 instances will be destroyed." {
		t.Fatalf("want the empty for_each warning, got %q", ws)
	}
	if !strings.Contains(pl.RenderText(false), "Warning: for_each for mock_thing.set is empty; all 2 instances will be destroyed.") {
		t.Fatalf("text plan must show it:\n%s", pl.RenderText(false))
	}
}

func TestWarningsEmptyForEachQuietWithNothingInState(t *testing.T) {
	pl, err := Build(context.Background(), mock.New(), state.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pl.EmptyForEach = []string{"mock_thing.set"}
	if ws := pl.Warnings(); ws != nil {
		t.Fatalf("nothing to destroy, nothing to warn about: %v", ws)
	}
}

func TestResolveImportsUndeclaredTargetMessages(t *testing.T) {
	ctx := context.Background()
	p := mock.New()
	pl, err := Build(ctx, p, state.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		`mock_thing.set["a"]`: "is not a declared instance",
		// A singular address gets the --generate-config hint.
		"mock_thing.solo": "plan --generate-config",
		// Unparseable addresses are reported as such, not as undeclared.
		"mock_thing.set[a]": "key must be a quoted string",
	}
	for address, want := range cases {
		err := ResolveImports(ctx, p, state.New(), pl, []ImportBlock{{Address: address, ID: "x"}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ResolveImports(%s) = %v, want error containing %q", address, err, want)
		}
	}
}
