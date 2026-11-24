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
	filteredText
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

func (g *gamePass) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Manages a game pass (a one-time, permanent in-experience purchase).",
		Attributes: map[string]string{
			"universe_id": "The universe. Often a reference to an experience.",
			"name":        "Game pass name.",
			"price":       "Price in Robux.",
			"description": "Game pass description.",
			"icon_file":   "Path to an icon image (uploaded directly). Change detected via content hash.",
			"is_for_sale": "On sale (default `true`).",
		},
		Notes:        "Game passes can't be hard-deleted, so `destroy` sets `is_for_sale = false` and unmanages it. The icon is reconciled via a content-hash trigger and re-uploaded only when its content changes, including when `icon_file` is added to an existing pass. Removing `icon_file` leaves the current icon in place. Managed pricing isn't exposed, because it requires prior manual acceptance of Roblox's managed-pricing terms in the Creator Dashboard.",
		Scopes:       []string{"game-pass:read", "game-pass:write"},
		TriggerAttrs: []string{"icon_file"},
		ExampleFile:  "examples/game-pass.bp.hcl",
	}
}

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
// resolved reference); and icon_file (reconciled via the content_hash trigger,
// not readable back). name/price/description/is_for_sale diff.
func (g *gamePass) Computed() []string {
	return []string{"id", "universe_id", "icon_file"}
}

func (g *gamePass) Dependencies(provider.Attributes) []provider.Ref { return nil }

// Triggers forces an Update (icon re-upload) when the icon file content changes.
// See developerProduct.Triggers for why a pass without icon_file records an
// empty content_hash.
func (g *gamePass) Triggers(desired provider.Attributes) (map[string]cty.Value, error) {
	return iconTriggers("roblox_game_pass", desired)
}

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

// Update is UpdateWithTriggers without the prior icon hash, so it re-uploads
// the icon.
func (g *gamePass) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	return g.UpdateWithTriggers(ctx, id, desired, nil)
}

// UpdateWithTriggers updates the pass's settings and sends the icon in the same
// request only when its content changed (see changedIcon).
func (g *gamePass) UpdateWithTriggers(ctx context.Context, id string, desired provider.Attributes, prior map[string]cty.Value) (provider.Attributes, error) {
	uid, gid, ok := splitGamePassID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_game_pass: invalid id %q", id)
	}
	update := opencloud.GamePassUpdate{
		Name:        desired["name"].AsString(),
		Description: stringOr(desired, "description"),
		Price:       numOr(desired, "price"),
		IsForSale:   boolOr(desired, "is_for_sale", true),
	}
	var err error
	if update.IconData, update.IconFile, update.IconType, err = changedIcon("roblox_game_pass", desired, prior); err != nil {
		return nil, err
	}
	if err := g.gc.Update(ctx, uid, gid, update); err != nil {
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
