package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type gamePass struct {
	gc opencloud.GamePassClient
}

// GamePass returns the roblox_game_pass resource. Its state id is
// "<universeID>/<gamePassID>" so Read/Update/Delete have both ids (the API is
// universe-scoped). universe_id is typically a reference to a roblox_experience's
// id. Game passes have no delete surface, so destroy takes the pass off sale.
func GamePass(gc opencloud.GamePassClient) provider.Resource {
	return &gamePass{gc: gc}
}

func (g *gamePass) Type() string { return "roblox_game_pass" }

func (g *gamePass) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"universe_id": &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"name":        &hcldec.AttrSpec{Name: "name", Type: cty.String, Required: true},
		"price":       &hcldec.AttrSpec{Name: "price", Type: cty.Number, Required: true},
		"description": &hcldec.AttrSpec{Name: "description", Type: cty.String, Required: false},
		"icon_file":   &hcldec.AttrSpec{Name: "icon_file", Type: cty.String, Required: false},
		"is_for_sale": &hcldec.AttrSpec{Name: "is_for_sale", Type: cty.Bool, Required: false},
	}
}

// Computed excludes from diff: the state id; universe_id (create-only, often a
// resolved reference); and icon_file (a create/update-only upload the API doesn't
// echo back). name/price/description/is_for_sale diff.
func (g *gamePass) Computed() []string {
	return []string{"id", "universe_id", "icon_file"}
}

func (g *gamePass) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (g *gamePass) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	universeID := desired["universe_id"].AsString()
	create := opencloud.GamePassCreate{
		Name:        desired["name"].AsString(),
		Description: stringOr(desired, "description"),
		Price:       numOr(desired, "price"),
		IsForSale:   boolOr(desired, "is_for_sale", true),
	}
	if p, ok := desired["icon_file"]; ok && !p.IsNull() {
		path := p.AsString()
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, fmt.Errorf("roblox_game_pass: reading icon_file %q: %w", path, err)
		}
		create.IconData, create.IconFile, create.IconType = data, filepath.Base(path), contentType(path)
	}
	got, err := g.gc.Create(ctx, universeID, create)
	if err != nil {
		return "", nil, err
	}
	return universeID + "/" + got.ID, g.stateAttrs(universeID, desired), nil
}

func (g *gamePass) Read(ctx context.Context, id string) (provider.Attributes, error) {
	uid, gid, ok := splitGamePassID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_game_pass: invalid id %q", id)
	}
	got, err := g.gc.Get(ctx, uid, gid)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	return provider.Attributes{
		"universe_id": cty.StringVal(uid),
		"name":        cty.StringVal(got.Name),
		"price":       cty.NumberIntVal(got.PriceInRobux),
		"description": cty.StringVal(got.Description),
		"is_for_sale": cty.BoolVal(got.IsForSale),
	}, nil
}

func (g *gamePass) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	uid, gid, ok := splitGamePassID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_game_pass: invalid id %q", id)
	}
	if err := g.gc.Update(ctx, uid, gid, opencloud.GamePassUpdate{
		Name:        desired["name"].AsString(),
		Description: stringOr(desired, "description"),
		Price:       numOr(desired, "price"),
		IsForSale:   boolOr(desired, "is_for_sale", true),
	}); err != nil {
		return nil, err
	}
	return g.stateAttrs(uid, desired), nil
}

func (g *gamePass) Delete(ctx context.Context, id string) error {
	uid, gid, ok := splitGamePassID(id)
	if !ok {
		return fmt.Errorf("roblox_game_pass: invalid id %q", id)
	}
	return g.gc.Delete(ctx, uid, gid)
}

func (g *gamePass) stateAttrs(universeID string, desired provider.Attributes) provider.Attributes {
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

func splitGamePassID(id string) (string, string, bool) {
	i := strings.IndexByte(id, '/')
	if i < 0 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}
