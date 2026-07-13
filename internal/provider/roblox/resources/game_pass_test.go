package resources

import (
	"context"
	"errors"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type fakeGamePass struct {
	created         opencloud.GamePassCreate
	createdUniverse string
	store           map[string]opencloud.GamePass
	getErr          error
}

func (f *fakeGamePass) Create(_ context.Context, universeID string, p opencloud.GamePassCreate) (opencloud.GamePass, error) {
	f.created, f.createdUniverse = p, universeID
	gp := opencloud.GamePass{ID: "555", Name: p.Name, Description: p.Description,
		PriceInRobux: p.Price, IsForSale: p.IsForSale}
	if f.store == nil {
		f.store = map[string]opencloud.GamePass{}
	}
	f.store["555"] = gp
	return gp, nil
}
func (f *fakeGamePass) Get(_ context.Context, _, id string) (opencloud.GamePass, error) {
	if f.getErr != nil {
		return opencloud.GamePass{}, f.getErr
	}
	return f.store[id], nil
}
func (f *fakeGamePass) Update(_ context.Context, _, id string, p opencloud.GamePassUpdate) error {
	gp := f.store[id]
	gp.Name, gp.PriceInRobux = p.Name, p.Price
	f.store[id] = gp
	return nil
}
func (f *fakeGamePass) Delete(_ context.Context, _, id string) error {
	gp := f.store[id]
	gp.IsForSale = false
	f.store[id] = gp
	return nil
}

func TestGamePassCreateAndReadRoundTrip(t *testing.T) {
	f := &fakeGamePass{}
	r := GamePass(f)

	id, attrs, err := r.Create(context.Background(), provider.Attributes{
		"universe_id": cty.StringVal("42"),
		"name":        cty.StringVal("VIP"),
		"price":       cty.NumberIntVal(100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "42/555" {
		t.Fatalf("id = %q, want 42/555", id)
	}
	if f.created.Name != "VIP" || f.created.Price != 100 || f.createdUniverse != "42" {
		t.Fatalf("create req = %+v (universe %q)", f.created, f.createdUniverse)
	}
	if attrs["universe_id"].AsString() != "42" {
		t.Fatalf("state attrs = %+v", attrs)
	}

	got, err := r.Read(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	price, _ := got["price"].AsBigFloat().Int64()
	if got["name"].AsString() != "VIP" || price != 100 || got["universe_id"].AsString() != "42" {
		t.Fatalf("read = %+v", got)
	}
}

func TestGamePassReadMapsNotFound(t *testing.T) {
	r := GamePass(&fakeGamePass{getErr: &opencloud.APIError{Status: 404}})
	if _, err := r.Read(context.Background(), "10/555"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want provider.ErrNotFound, got %v", err)
	}
}
