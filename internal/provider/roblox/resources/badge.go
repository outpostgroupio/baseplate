package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type badge struct {
	bc opencloud.BadgeClient
}

// Badge returns the roblox_badge resource. Its state id is "<universeID>/<badgeID>".
// Badges have no delete surface, so destroy disables the badge (enabled=false).
func Badge(bc opencloud.BadgeClient) provider.Resource { return &badge{bc: bc} }

func (b *badge) Type() string { return "roblox_badge" }

func (b *badge) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Manages a badge (an achievement players can earn in an experience).",
		Attributes: map[string]string{
			"universe_id":    "The universe. Often a reference to an experience.",
			"name":           "Badge name.",
			"description":    "Badge description.",
			"enabled":        "Whether the badge is active and awardable (default `true`).",
			"icon_file":      "Path to an icon image (direct upload, required — Roblox rejects a badge created without an icon). Change detected via content hash.",
			"expected_cost":  "Robux cost you accept for creating the badge (default `0`). Creation is free in practice; a non-zero value opts into a paid badge.",
			"payment_source": "Funding source when `expected_cost` > 0: `user` (default) or `group`.",
		},
		Notes:        "Badges can't be hard-deleted, so `destroy` sets `enabled = false` and unmanages the badge. The icon is reconciled via a content-hash trigger and re-uploaded only when its content changes. Reads use the public badge endpoint on `badges.roblox.com` (writes use Open Cloud on `apis.roblox.com`).",
		Scopes:       []string{"legacy-universe.badge:manage-and-spend-robux", "legacy-universe.badge:write", "legacy-badge:manage"},
		TriggerAttrs: []string{"icon_file"},
		ExampleFile:  "examples/badge.bp.hcl",
	}
}

func (b *badge) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"universe_id":    &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"name":           &hcldec.AttrSpec{Name: "name", Type: cty.String, Required: true},
		"description":    &hcldec.AttrSpec{Name: "description", Type: cty.String, Required: false},
		"enabled":        &hcldec.AttrSpec{Name: "enabled", Type: cty.Bool, Required: false},
		"icon_file":      &hcldec.AttrSpec{Name: "icon_file", Type: cty.String, Required: true},
		"expected_cost":  &hcldec.AttrSpec{Name: "expected_cost", Type: cty.Number, Required: false},
		"payment_source": &hcldec.AttrSpec{Name: "payment_source", Type: cty.String, Required: false},
	}
}

// Computed excludes from diff: the state id; universe_id (create-only, often a
// resolved reference); icon_file (reconciled via the content_hash trigger, not
// readable back); and the create-only expected_cost/payment_source. name,
// description, and enabled diff normally.
func (b *badge) Computed() []string {
	return []string{"id", "universe_id", "icon_file", "expected_cost", "payment_source"}
}

func (b *badge) Dependencies(provider.Attributes) []provider.Ref { return nil }

// Triggers forces an Update (icon re-upload) when the icon file content changes.
// icon_file is Required, so it is always present (mirrors roblox_place's file).
func (b *badge) Triggers(desired provider.Attributes) (map[string]cty.Value, error) {
	sum, err := hashFile("roblox_badge", "icon_file", desired["icon_file"].AsString())
	if err != nil {
		return nil, err
	}
	return map[string]cty.Value{"content_hash": cty.StringVal(sum)}, nil
}

func (b *badge) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	universeID := desired["universe_id"].AsString()
	create := opencloud.BadgeCreate{
		Name:              desired["name"].AsString(),
		Description:       stringOr(desired, "description"),
		IsActive:          boolOr(desired, "enabled", true),
		PaymentSourceType: paymentSourceType(desired),
		ExpectedCost:      numOr(desired, "expected_cost"),
	}
	// icon_file is Required; Roblox rejects a badge created without an icon.
	path := desired["icon_file"].AsString()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("roblox_badge: reading icon_file %q: %w", path, err)
	}
	create.IconData, create.IconFile, create.IconType = data, filepath.Base(path), contentType(path)
	got, err := b.bc.Create(ctx, universeID, create)
	if err != nil {
		return "", nil, err
	}
	return universeID + "/" + got.ID, b.stateAttrs(universeID, desired), nil
}

func (b *badge) Read(ctx context.Context, id string) (provider.Attributes, error) {
	uid, bid, ok := splitBadgeID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_badge: invalid id %q", id)
	}
	got, err := b.bc.Get(ctx, uid, bid)
	if err != nil {
		if opencloud.ErrBadgeNotFound(err) || opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	return provider.Attributes{
		"universe_id": cty.StringVal(uid),
		"name":        cty.StringVal(got.Name),
		"description": cty.StringVal(got.Description),
		"enabled":     cty.BoolVal(got.Enabled),
	}, nil
}

// Update is UpdateWithTriggers without the prior icon hash, so it re-uploads
// the icon.
func (b *badge) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	return b.UpdateWithTriggers(ctx, id, desired, nil)
}

// UpdateWithTriggers updates name/description/enabled and re-uploads the icon
// only when its content_hash differs from the one recorded before this update
// (prior), so a name-only edit doesn't re-upload (and re-moderate) the icon. A
// missing icon file skips the upload: its content can't have been checked.
func (b *badge) UpdateWithTriggers(ctx context.Context, id string, desired provider.Attributes, prior map[string]cty.Value) (provider.Attributes, error) {
	uid, bid, ok := splitBadgeID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_badge: invalid id %q", id)
	}
	if err := b.bc.Update(ctx, bid, opencloud.BadgeUpdate{
		Name:        desired["name"].AsString(),
		Description: stringOr(desired, "description"),
		Enabled:     boolOr(desired, "enabled", true),
	}); err != nil {
		return nil, err
	}
	path := desired["icon_file"].AsString()
	sum, err := hashFile("roblox_badge", "icon_file", path)
	var unavailable *provider.TriggerUnavailableError
	if errors.As(err, &unavailable) || (err == nil && !contentChanged(prior, sum)) {
		return b.stateAttrs(uid, desired), nil
	}
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("roblox_badge: reading icon_file %q: %w", path, err)
	}
	if err := b.bc.UploadIcon(ctx, bid, data, filepath.Base(path), contentType(path)); err != nil {
		return nil, err
	}
	return b.stateAttrs(uid, desired), nil
}

// Delete disables the badge (badges cannot be hard-deleted).
func (b *badge) Delete(ctx context.Context, id string) error {
	uid, bid, ok := splitBadgeID(id)
	if !ok {
		return fmt.Errorf("roblox_badge: invalid id %q", id)
	}
	_ = uid
	return b.bc.Update(ctx, bid, opencloud.BadgeUpdate{Enabled: false})
}

func (b *badge) stateAttrs(universeID string, desired provider.Attributes) provider.Attributes {
	out := provider.Attributes{
		"universe_id": cty.StringVal(universeID),
		"name":        desired["name"],
	}
	for _, k := range []string{"description", "enabled", "icon_file", "expected_cost", "payment_source"} {
		if v, ok := desired[k]; ok && !v.IsNull() {
			out[k] = v
		}
	}
	return out
}

// paymentSourceType maps payment_source ("user"/"group") to the API enum (1/2);
// defaults to User.
func paymentSourceType(desired provider.Attributes) int {
	if v, ok := desired["payment_source"]; ok && !v.IsNull() && strings.EqualFold(v.AsString(), "group") {
		return 2
	}
	return 1
}

// splitBadgeID splits "<universeID>/<badgeID>".
func splitBadgeID(id string) (string, string, bool) {
	i := strings.IndexByte(id, '/')
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}
