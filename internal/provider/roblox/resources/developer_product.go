package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/roblox/opencloud"
)

type developerProduct struct {
	pc opencloud.DeveloperProductClient
}

// DeveloperProduct returns the roblox_developer_product resource. Its state id is
// "<universeID>/<productID>" so Read/Update/Delete have both ids (the API is
// scoped by universe). universe_id is typically a reference to a
// roblox_experience's id.
func DeveloperProduct(pc opencloud.DeveloperProductClient) provider.Resource {
	return &developerProduct{pc: pc}
}

func (d *developerProduct) Type() string { return "roblox_developer_product" }

func (d *developerProduct) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"universe_id": &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"name":        &hcldec.AttrSpec{Name: "name", Type: cty.String, Required: true},
		"price":       &hcldec.AttrSpec{Name: "price", Type: cty.Number, Required: true},
		"description": &hcldec.AttrSpec{Name: "description", Type: cty.String, Required: false},
		"icon_file":   &hcldec.AttrSpec{Name: "icon_file", Type: cty.String, Required: false},
		"is_for_sale": &hcldec.AttrSpec{Name: "is_for_sale", Type: cty.Bool, Required: false},
	}
}

// Computed excludes from diff: the state id; universe_id (create-only — a product
// can't move universes — and often a resolved reference); and icon_file (a
// create/update-only upload the API doesn't echo back). name/price/description/
// is_for_sale remain diffable.
func (d *developerProduct) Computed() []string {
	return []string{"id", "universe_id", "icon_file"}
}

func (d *developerProduct) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (d *developerProduct) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	universeID := desired["universe_id"].AsString()
	create := opencloud.DeveloperProductCreate{
		Name:        desired["name"].AsString(),
		Price:       numOr(desired, "price"),
		Description: stringOr(desired, "description"),
		IsForSale:   boolOr(desired, "is_for_sale", true),
	}
	if p, ok := desired["icon_file"]; ok && !p.IsNull() {
		path := p.AsString()
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, fmt.Errorf("roblox_developer_product: reading icon_file %q: %w", path, err)
		}
		create.IconData, create.IconFile, create.IconType = data, filepath.Base(path), contentType(path)
	}
	got, err := d.pc.Create(ctx, universeID, create)
	if err != nil {
		return "", nil, err
	}
	return universeID + "/" + got.ID, d.stateAttrs(universeID, desired), nil
}

func (d *developerProduct) Read(ctx context.Context, id string) (provider.Attributes, error) {
	uid, pid, ok := splitProductID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_developer_product: invalid id %q", id)
	}
	got, err := d.pc.Get(ctx, uid, pid)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	return provider.Attributes{
		"name":        cty.StringVal(got.Name),
		"price":       cty.NumberIntVal(got.PriceInRobux),
		"description": cty.StringVal(got.Description),
		"is_for_sale": cty.BoolVal(got.IsForSale),
	}, nil
}

func (d *developerProduct) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	uid, pid, ok := splitProductID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_developer_product: invalid id %q", id)
	}
	if _, err := d.pc.Update(ctx, uid, pid, opencloud.DeveloperProductUpdate{
		Name:        desired["name"].AsString(),
		Price:       numOr(desired, "price"),
		Description: stringOr(desired, "description"),
		IsForSale:   boolOr(desired, "is_for_sale", true),
	}); err != nil {
		return nil, err
	}
	return d.stateAttrs(uid, desired), nil
}

func (d *developerProduct) Delete(ctx context.Context, id string) error {
	uid, pid, ok := splitProductID(id)
	if !ok {
		return fmt.Errorf("roblox_developer_product: invalid id %q", id)
	}
	return d.pc.Delete(ctx, uid, pid)
}

func (d *developerProduct) stateAttrs(universeID string, desired provider.Attributes) provider.Attributes {
	out := provider.Attributes{
		"universe_id": cty.StringVal(universeID),
		"name":        desired["name"],
		"price":       desired["price"],
	}
	for _, k := range []string{"description", "is_for_sale", "icon_file"} {
		if v, ok := desired[k]; ok && !v.IsNull() {
			out[k] = v
		}
	}
	return out
}

func splitProductID(id string) (string, string, bool) {
	i := strings.IndexByte(id, '/')
	if i < 0 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

func stringOr(desired provider.Attributes, k string) string {
	if v, ok := desired[k]; ok && !v.IsNull() {
		return v.AsString()
	}
	return ""
}

func boolOr(desired provider.Attributes, k string, def bool) bool {
	if v, ok := desired[k]; ok && !v.IsNull() {
		return v.True()
	}
	return def
}
