package plan

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/mock"
	"github.com/outpostgroup/baseplate/internal/state"
)

func TestBuildCreateAndOrder(t *testing.T) {
	p := mock.New()
	s := state.New()
	desired := []DesiredResource{
		{Type: "mock_child", Name: "c", Attributes: provider.Attributes{
			"name": cty.StringVal("child"), "parent": cty.StringVal("mock_thing.a")},
			DependsOn: []string{"mock_thing.a"}},
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
	}
	pl, err := Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !pl.HasChanges() {
		t.Fatal("expected changes")
	}
	if pl.Changes[0].Address != "mock_thing.a" {
		t.Fatalf("expected parent first, got %s", pl.Changes[0].Address)
	}
	for _, c := range pl.Changes {
		if c.Action != provider.ActionCreate {
			t.Fatalf("%s: expected create, got %s", c.Address, c.Action)
		}
	}
}
