package apply

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/engine/plan"
	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/mock"
	"github.com/outpostgroup/baseplate/internal/state"
	"github.com/outpostgroup/baseplate/internal/state/backend"
)

func TestApplyCreatesAndPersists(t *testing.T) {
	p := mock.New()
	s := state.New()
	desired := []plan.DesiredResource{
		{Type: "mock_thing", Name: "a", Attributes: provider.Attributes{"name": cty.StringVal("root")}},
	}
	pl, err := plan.Build(context.Background(), p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "baseplate.tfstate"))
	if err := Apply(context.Background(), p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	if len(p.Store()) != 1 {
		t.Fatalf("expected 1 remote object, got %d", len(p.Store()))
	}
	persisted, _ := b.Get()
	if _, ok := persisted.Find("mock_thing.a"); !ok {
		t.Fatal("state not persisted after apply")
	}
}
