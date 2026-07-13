package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type place struct{ pc opencloud.PlaceClient }

// Place returns the roblox_place resource. Its state id is "<universeID>/<placeID>"
// (publish is scoped by universe). It publishes an existing place's content; it
// does not create places.
func Place(pc opencloud.PlaceClient) provider.Resource { return &place{pc: pc} }

func (p *place) Type() string { return "roblox_place" }

func (p *place) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Publishes a place file (`.rbxl`/`.rbxlx`) to an existing place and re-publishes when the file content changes.",
		Attributes: map[string]string{
			"universe_id":  "The universe the place belongs to.",
			"place_id":     "The existing place to publish to.",
			"file":         "Path to the `.rbxl`/`.rbxlx`. Change detected via content hash.",
			"version_type": "`Published` (default) or `Saved`.",
		},
		Notes:  "Place content isn't remotely readable, so change detection is by file content hash. `destroy` is a no-op (a place can't be unpublished).",
		Scopes: []string{"universe-places:write"},
	}
}

func (p *place) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"universe_id":  &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"place_id":     &hcldec.AttrSpec{Name: "place_id", Type: cty.String, Required: true},
		"file":         &hcldec.AttrSpec{Name: "file", Type: cty.String, Required: true},
		"version_type": &hcldec.AttrSpec{Name: "version_type", Type: cty.String, Required: false},
	}
}

// Computed excludes every declared attribute from the normal diff: a place has no
// remotely-readable mutable content, so all change detection flows through
// Triggers (file content hash + version_type). version_number is a provider output.
func (p *place) Computed() []string {
	return []string{"id", "universe_id", "place_id", "file", "version_type", "version_number"}
}

func (p *place) Dependencies(provider.Attributes) []provider.Ref { return nil }

// Triggers forces a re-publish when the file content or version_type changes.
func (p *place) Triggers(desired provider.Attributes) (map[string]cty.Value, error) {
	data, err := os.ReadFile(desired["file"].AsString())
	if err != nil {
		return nil, fmt.Errorf("roblox_place: reading file: %w", err)
	}
	sum := sha256.Sum256(data)
	return map[string]cty.Value{
		"content_hash": cty.StringVal(hex.EncodeToString(sum[:])),
		"version_type": cty.StringVal(versionTypeOr(desired)),
	}, nil
}

func (p *place) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	universeID := desired["universe_id"].AsString()
	placeID := desired["place_id"].AsString()
	version, err := p.publish(ctx, universeID, placeID, desired)
	if err != nil {
		return "", nil, err
	}
	return universeID + "/" + placeID, p.stateAttrs(universeID, placeID, desired, version), nil
}

func (p *place) Read(ctx context.Context, id string) (provider.Attributes, error) {
	uid, pid, ok := splitPlaceID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_place: invalid id %q", id)
	}
	// Place content is not remotely readable; return identity only. Change
	// detection is handled by Triggers, so no mutable attribute is reported here.
	return provider.Attributes{
		"universe_id": cty.StringVal(uid),
		"place_id":    cty.StringVal(pid),
	}, nil
}

func (p *place) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	uid, pid, ok := splitPlaceID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_place: invalid id %q", id)
	}
	version, err := p.publish(ctx, uid, pid, desired)
	if err != nil {
		return nil, err
	}
	return p.stateAttrs(uid, pid, desired, version), nil
}

// Delete is a no-op: a place cannot be unpublished, so destroy stops managing it.
func (p *place) Delete(ctx context.Context, id string) error { return nil }

func (p *place) publish(ctx context.Context, universeID, placeID string, desired provider.Attributes) (int64, error) {
	path := desired["file"].AsString()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("roblox_place: reading file %q: %w", path, err)
	}
	return p.pc.Publish(ctx, universeID, placeID, versionTypeOr(desired), placeContentType(path), data)
}

func (p *place) stateAttrs(universeID, placeID string, desired provider.Attributes, version int64) provider.Attributes {
	return provider.Attributes{
		"universe_id":    cty.StringVal(universeID),
		"place_id":       cty.StringVal(placeID),
		"file":           desired["file"],
		"version_type":   cty.StringVal(versionTypeOr(desired)),
		"version_number": cty.NumberIntVal(version),
	}
}

func versionTypeOr(desired provider.Attributes) string {
	if v, ok := desired["version_type"]; ok && !v.IsNull() {
		return v.AsString()
	}
	return "Published"
}

// placeContentType picks the raw upload Content-Type by extension: .rbxlx is XML,
// everything else (.rbxl) is treated as binary.
func placeContentType(path string) string {
	if strings.HasSuffix(strings.ToLower(path), ".rbxlx") {
		return "application/xml"
	}
	return "application/octet-stream"
}

// splitPlaceID splits "<universeID>/<placeID>".
func splitPlaceID(id string) (string, string, bool) {
	i := strings.IndexByte(id, '/')
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}
