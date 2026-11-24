package resources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type fakeProduct struct {
	created         opencloud.DeveloperProductCreate
	createdUniverse string
	updates         []opencloud.DeveloperProductUpdate
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
	f.updates = append(f.updates, p)
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

func TestDeveloperProductRegionalPricing(t *testing.T) {
	f := &fakeProduct{store: map[string]opencloud.DeveloperProduct{
		"777": {ID: "777", Name: "Gems", PriceInRobux: 25, RegionalPricingEnabled: true},
	}}
	r := DeveloperProduct(f)

	base := provider.Attributes{
		"universe_id": cty.StringVal("42"),
		"name":        cty.StringVal("Gems"),
		"price":       cty.NumberIntVal(25),
	}
	if _, _, err := r.Create(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if f.created.RegionalPricing != nil {
		t.Fatalf("unset is_regional_pricing_enabled must not be sent; got %v", *f.created.RegionalPricing)
	}

	desired := provider.Attributes{"is_regional_pricing_enabled": cty.False}
	for k, v := range base {
		desired[k] = v
	}
	attrs, err := r.Update(context.Background(), "42/777", desired)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.updates[0].RegionalPricing; got == nil || *got {
		t.Fatalf("update must send isRegionalPricingEnabled=false; got %v", got)
	}
	if attrs["is_regional_pricing_enabled"] != cty.False {
		t.Fatalf("state attrs = %+v", attrs)
	}

	f.store["777"] = opencloud.DeveloperProduct{ID: "777", Name: "Gems", RegionalPricingEnabled: true}
	got, err := r.Read(context.Background(), "42/777")
	if err != nil {
		t.Fatal(err)
	}
	if got["is_regional_pricing_enabled"] != cty.True {
		t.Fatalf("read = %+v", got)
	}
}

func TestDeveloperProductIconUploadedOnlyWhenChanged(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(fp, []byte("PNG1"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeProduct{store: map[string]opencloud.DeveloperProduct{"777": {ID: "777"}}}
	r := DeveloperProduct(f).(*developerProduct)
	desired := provider.Attributes{
		"universe_id": cty.StringVal("42"),
		"name":        cty.StringVal("Gems"),
		"price":       cty.NumberIntVal(25),
		"icon_file":   cty.StringVal(fp),
	}
	trig, err := r.Triggers(desired)
	if err != nil {
		t.Fatal(err)
	}

	// Same hash as recorded: a metadata-only edit sends no icon.
	if _, err := r.UpdateWithTriggers(context.Background(), "42/777", desired, trig); err != nil {
		t.Fatal(err)
	}
	if len(f.updates[0].IconData) != 0 {
		t.Fatal("unchanged icon must not be re-uploaded")
	}

	// New content: the icon goes in the same PATCH.
	if err := os.WriteFile(fp, []byte("PNG2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpdateWithTriggers(context.Background(), "42/777", desired, trig); err != nil {
		t.Fatal(err)
	}
	if u := f.updates[1]; string(u.IconData) != "PNG2" || u.IconFile != "icon.png" || u.IconType != "image/png" {
		t.Fatalf("changed icon must be uploaded; got %q %q %q", u.IconData, u.IconFile, u.IconType)
	}
}

// A product without icon_file records an empty hash, so adding icon_file later
// diffs and uploads instead of being adopted as the baseline.
func TestDeveloperProductAddingIconDiffs(t *testing.T) {
	r := DeveloperProduct(&fakeProduct{}).(*developerProduct)
	without, err := r.Triggers(provider.Attributes{"name": cty.StringVal("Gems")})
	if err != nil {
		t.Fatal(err)
	}
	if without["content_hash"] != cty.StringVal("") {
		t.Fatalf("triggers without icon_file = %+v", without)
	}

	fp := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(fp, []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	with, err := r.Triggers(provider.Attributes{"icon_file": cty.StringVal(fp)})
	if err != nil {
		t.Fatal(err)
	}
	if with["content_hash"].RawEquals(without["content_hash"]) {
		t.Fatal("adding icon_file must change content_hash")
	}
}
