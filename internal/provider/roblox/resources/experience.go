package resources

import (
	"context"
	"sort"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/roblox/opencloud"
)

// experienceBoolWire maps HCL attribute names to universe wire field names.
var experienceBoolWire = map[string]string{
	"voice_chat_enabled": "voiceChatEnabled",
	"desktop_enabled":    "desktopEnabled",
	"mobile_enabled":     "mobileEnabled",
	"tablet_enabled":     "tabletEnabled",
}

type experience struct{ uc opencloud.UniverseClient }

// Experience returns the roblox_experience resource backed by a UniverseClient.
func Experience(uc opencloud.UniverseClient) provider.Resource { return &experience{uc: uc} }

func (e *experience) Type() string { return "roblox_experience" }

func (e *experience) DecodeSpec() hcldec.Spec {
	spec := hcldec.ObjectSpec{
		"universe_id": &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
	}
	for name := range experienceBoolWire {
		spec[name] = &hcldec.AttrSpec{Name: name, Type: cty.Bool, Required: false}
	}
	return spec
}

func (e *experience) Computed() []string                              { return []string{"id"} }
func (e *experience) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (e *experience) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	id := desired["universe_id"].AsString()
	u, mask := universeFromDesired(id, desired)
	if len(mask) > 0 {
		if _, err := e.uc.Update(ctx, u, mask); err != nil {
			return "", nil, err
		}
	}
	got, err := e.uc.Get(ctx, id)
	if err != nil {
		return "", nil, err
	}
	return id, universeAttrs(id, got), nil
}

func (e *experience) Read(ctx context.Context, id string) (provider.Attributes, error) {
	got, err := e.uc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return universeAttrs(id, got), nil
}

func (e *experience) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	u, mask := universeFromDesired(id, desired)
	if len(mask) > 0 {
		if _, err := e.uc.Update(ctx, u, mask); err != nil {
			return nil, err
		}
	}
	got, err := e.uc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return universeAttrs(id, got), nil
}

// Delete is a no-op: Baseplate adopts an existing universe's config but does
// not own its lifecycle, so dropping the resource just stops managing it.
func (e *experience) Delete(ctx context.Context, id string) error { return nil }

func universeFromDesired(id string, desired provider.Attributes) (opencloud.Universe, []string) {
	u := opencloud.Universe{ID: id}
	var mask []string
	for attr, wire := range experienceBoolWire {
		v, ok := desired[attr]
		if !ok || v.IsNull() {
			continue
		}
		b := v.True()
		switch attr {
		case "voice_chat_enabled":
			u.VoiceChatEnabled = &b
		case "desktop_enabled":
			u.DesktopEnabled = &b
		case "mobile_enabled":
			u.MobileEnabled = &b
		case "tablet_enabled":
			u.TabletEnabled = &b
		}
		mask = append(mask, wire)
	}
	sort.Strings(mask)
	return u, mask
}

func universeAttrs(id string, u opencloud.Universe) provider.Attributes {
	a := provider.Attributes{"universe_id": cty.StringVal(id)}
	if u.VoiceChatEnabled != nil {
		a["voice_chat_enabled"] = cty.BoolVal(*u.VoiceChatEnabled)
	}
	if u.DesktopEnabled != nil {
		a["desktop_enabled"] = cty.BoolVal(*u.DesktopEnabled)
	}
	if u.MobileEnabled != nil {
		a["mobile_enabled"] = cty.BoolVal(*u.MobileEnabled)
	}
	if u.TabletEnabled != nil {
		a["tablet_enabled"] = cty.BoolVal(*u.TabletEnabled)
	}
	return a
}
