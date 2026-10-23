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
	for _, want := range []string{"mock_thing.set:", "1 instance(s) will be destroyed and 1 created",
		`baseplate import 'mock_thing.set["icons/gem.png"]' mock_thing/1`} {
		if !strings.Contains(ws[0], want) {
			t.Fatalf("warning missing %q:\n%s", want, ws[0])
		}
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
