package resources

import (
	"context"
	"errors"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type fakeProduct struct {
	created         opencloud.DeveloperProductCreate
	createdUniverse string
	store           map[string]opencloud.DeveloperProduct
	getErr          error
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
	if f.getErr != nil {
		return opencloud.DeveloperProduct{}, f.getErr
	}
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

func TestDeveloperProductReadMapsNotFound(t *testing.T) {
	r := DeveloperProduct(&fakeProduct{getErr: &opencloud.APIError{Status: 404}})
	if _, err := r.Read(context.Background(), "10/777"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want provider.ErrNotFound, got %v", err)
	}
}

func TestDeveloperProductReadIncludesUniverseID(t *testing.T) {
	f := &fakeProduct{store: map[string]opencloud.DeveloperProduct{
		"777": {ID: "777", Name: "Gems", PriceInRobux: 25, IsForSale: true},
	}}
	attrs, err := DeveloperProduct(f).Read(context.Background(), "42/777")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := attrs["universe_id"]; !ok || v.AsString() != "42" {
		t.Fatalf("Read must include universe_id from the id; got %+v", attrs)
	}
}
