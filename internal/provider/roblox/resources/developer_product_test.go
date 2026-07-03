package resources

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/roblox/opencloud"
)

type fakeProduct struct {
	created         opencloud.DeveloperProductCreate
	createdUniverse string
	store           map[string]opencloud.DeveloperProduct
}

func (f *fakeProduct) Create(_ context.Context, universeID string, p opencloud.DeveloperProductCreate) (opencloud.DeveloperProduct, error) {
	f.created, f.createdUniverse = p, universeID
	dp := opencloud.DeveloperProduct{ID: "777", Name: p.Name, PriceInRobux: p.Price,
		UniverseID: universeID, IsForSale: p.IsForSale, Description: p.Description}
	if f.store == nil {
		f.store = map[string]opencloud.DeveloperProduct{}
	}
	f.store["777"] = dp
	return dp, nil
}
func (f *fakeProduct) Get(_ context.Context, _, productID string) (opencloud.DeveloperProduct, error) {
	return f.store[productID], nil
}
func (f *fakeProduct) Update(_ context.Context, _, productID string, p opencloud.DeveloperProductUpdate) (opencloud.DeveloperProduct, error) {
	dp := f.store[productID]
	dp.Name, dp.PriceInRobux = p.Name, p.Price
	f.store[productID] = dp
	return dp, nil
}
func (f *fakeProduct) Delete(_ context.Context, _, productID string) error {
	delete(f.store, productID)
	return nil
}

func TestDeveloperProductCreateAndReadRoundTrip(t *testing.T) {
	f := &fakeProduct{}
	r := DeveloperProduct(f)

	id, attrs, err := r.Create(context.Background(), provider.Attributes{
		"universe_id": cty.StringVal("42"),
		"name":        cty.StringVal("100 Gems"),
		"price":       cty.NumberIntVal(99),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "42/777" {
		t.Fatalf("id = %q, want 42/777", id)
	}
	if f.created.Name != "100 Gems" || f.created.Price != 99 || f.createdUniverse != "42" {
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
	if got["name"].AsString() != "100 Gems" || price != 99 {
		t.Fatalf("read = %+v", got)
	}
}
