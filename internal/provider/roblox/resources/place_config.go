package resources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// placeConfigWire maps HCL attribute names to Place wire field names (the
// updateMask entries).
var placeConfigWire = map[string]string{
	"display_name": "displayName",
	"description":  "description",
	"server_size":  "serverSize",
}

type placeConfig struct {
	filteredText
	pc opencloud.PlaceConfigClient
}

// PlaceConfig returns the roblox_place_config resource. Its state id is
// "<universeID>/<placeID>". It configures an existing place and is independent of
// roblox_place, so a config change never re-publishes the place file.
func PlaceConfig(pc opencloud.PlaceConfigClient) provider.Resource { return &placeConfig{pc: pc} }

func (p *placeConfig) Type() string { return "roblox_place_config" }

func (p *placeConfig) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Manages an existing place's configuration (name, description, server size). Adopts the place; it does not create places.",
		Attributes: map[string]string{
			"universe_id":  "The universe the place belongs to.",
			"place_id":     "The place to configure.",
			"display_name": "The place's name. On the root place this is also the experience's name.",
			"description":  "The place's description. On the root place this is also the experience's description.",
			"server_size":  "The maximum number of players in a single server.",
		},
		Notes:       "Only declared attributes are managed; omitted ones are left as they are in Roblox. The experience's display name and description are read-only on the universe API, so set them here on the root place. `destroy` unmanages the place (nothing is reverted or deleted). Roblox rejects changes (409) while the place is open in Team Create. Independent of `roblox_place`, which publishes the place file.",
		Scopes:      []string{"universe.place:read", "universe.place:write"},
		ExampleFile: "examples/baseplate.bp.hcl",
	}
}

func (p *placeConfig) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"universe_id":  &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"place_id":     &hcldec.AttrSpec{Name: "place_id", Type: cty.String, Required: true},
		"display_name": &hcldec.AttrSpec{Name: "display_name", Type: cty.String, Required: false},
		"description":  &hcldec.AttrSpec{Name: "description", Type: cty.String, Required: false},
		"server_size":  &hcldec.AttrSpec{Name: "server_size", Type: cty.Number, Required: false},
	}
}

// Computed excludes the identity attrs from the diff: universe_id may be a
// reference resolved at apply time, and both are fixed by the state id.
func (p *placeConfig) Computed() []string { return []string{"id", "universe_id", "place_id"} }

func (p *placeConfig) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (p *placeConfig) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	uid := desired["universe_id"].AsString()
	pid := desired["place_id"].AsString()
	attrs, err := p.apply(ctx, uid, pid, desired)
	if err != nil {
		return "", nil, err
	}
	return uid + "/" + pid, attrs, nil
}

func (p *placeConfig) Read(ctx context.Context, id string) (provider.Attributes, error) {
	uid, pid, ok := splitPlaceID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_place_config: invalid id %q", id)
	}
	got, err := p.pc.Get(ctx, uid, pid)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	return placeConfigAttrs(uid, pid, got), nil
}

func (p *placeConfig) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	uid, pid, ok := splitPlaceID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_place_config: invalid id %q", id)
	}
	return p.apply(ctx, uid, pid, desired)
}

// Delete is a no-op: Baseplate configures an existing place but does not own its
// lifecycle, so dropping the resource just stops managing it.
func (p *placeConfig) Delete(ctx context.Context, id string) error { return nil }

// apply PATCHes the declared attributes (if any) and reads the place back.
func (p *placeConfig) apply(ctx context.Context, uid, pid string, desired provider.Attributes) (provider.Attributes, error) {
	cfg, mask := placeConfigFromDesired(desired)
	if len(mask) > 0 {
		if _, err := p.pc.Update(ctx, uid, pid, cfg, mask); err != nil {
			var apiErr *opencloud.APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
				return nil, fmt.Errorf("roblox_place_config: place %s is open in an active Team Create session; close it and re-run apply: %w", pid, err)
			}
			return nil, err
		}
	}
	got, err := p.pc.Get(ctx, uid, pid)
	if err != nil {
		return nil, err
	}
	return placeConfigAttrs(uid, pid, got), nil
}

func placeConfigFromDesired(desired provider.Attributes) (opencloud.PlaceConfig, []string) {
	var cfg opencloud.PlaceConfig
	var mask []string
	for attr, wire := range placeConfigWire {
		v, ok := desired[attr]
		if !ok || v.IsNull() {
			continue
		}
		switch attr {
		case "display_name":
			s := v.AsString()
			cfg.DisplayName = &s
		case "description":
			s := v.AsString()
			cfg.Description = &s
		case "server_size":
			n, _ := v.AsBigFloat().Int64()
			cfg.ServerSize = &n
		}
		mask = append(mask, wire)
	}
	sort.Strings(mask)
	return cfg, mask
}

func placeConfigAttrs(uid, pid string, c opencloud.PlaceConfig) provider.Attributes {
	a := provider.Attributes{
		"universe_id": cty.StringVal(uid),
		"place_id":    cty.StringVal(pid),
	}
	if c.DisplayName != nil {
		a["display_name"] = cty.StringVal(*c.DisplayName)
	}
	if c.Description != nil {
		a["description"] = cty.StringVal(*c.Description)
	}
	if c.ServerSize != nil {
		a["server_size"] = cty.NumberIntVal(*c.ServerSize)
	}
	return a
}
