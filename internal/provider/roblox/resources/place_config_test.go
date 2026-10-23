package resources

import (
	"context"
	"errors"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type fakePlaceConfig struct {
	store  map[string]opencloud.PlaceConfig
	masks  [][]string
	getErr error
}

func (f *fakePlaceConfig) Get(_ context.Context, uid, pid string) (opencloud.PlaceConfig, error) {
	if f.getErr != nil {
		return opencloud.PlaceConfig{}, f.getErr
	}
	return f.store[uid+"/"+pid], nil
}

func (f *fakePlaceConfig) Update(_ context.Context, uid, pid string, p opencloud.PlaceConfig, mask []string) (opencloud.PlaceConfig, error) {
	f.masks = append(f.masks, mask)
	cur := f.store[uid+"/"+pid]
	if p.DisplayName != nil {
		cur.DisplayName = p.DisplayName
	}
	if p.Description != nil {
		cur.Description = p.Description
	}
	if p.ServerSize != nil {
		cur.ServerSize = p.ServerSize
	}
	f.store[uid+"/"+pid] = cur
	return cur, nil
}

func strp(s string) *string { return &s }
func i64p(n int64) *int64   { return &n }

func TestPlaceConfigCreatePatchesOnlyDeclaredAttrs(t *testing.T) {
	f := &fakePlaceConfig{store: map[string]opencloud.PlaceConfig{
		"1/2": {DisplayName: strp("Old"), Description: strp("keep me"), ServerSize: i64p(50)},
	}}
	r := PlaceConfig(f)

	id, attrs, err := r.Create(context.Background(), provider.Attributes{
		"universe_id":  cty.StringVal("1"),
		"place_id":     cty.StringVal("2"),
		"display_name": cty.StringVal("OpenFront"),
		"server_size":  cty.NumberIntVal(12),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "1/2" {
		t.Fatalf("id = %q", id)
	}
	if got := f.masks[0]; len(got) != 2 || got[0] != "displayName" || got[1] != "serverSize" {
		t.Fatalf("mask = %+v", got)
	}
	if attrs["display_name"].AsString() != "OpenFront" || attrs["description"].AsString() != "keep me" {
		t.Fatalf("attrs = %+v", attrs)
	}
	if n, _ := attrs["server_size"].AsBigFloat().Int64(); n != 12 {
		t.Fatalf("server_size = %v", attrs["server_size"])
	}
}

func TestPlaceConfigNoDeclaredAttrsSkipsPatch(t *testing.T) {
	f := &fakePlaceConfig{store: map[string]opencloud.PlaceConfig{"1/2": {ServerSize: i64p(50)}}}
	_, _, err := PlaceConfig(f).Create(context.Background(), provider.Attributes{
		"universe_id": cty.StringVal("1"),
		"place_id":    cty.StringVal("2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.masks) != 0 {
		t.Fatalf("expected no PATCH, got masks %+v", f.masks)
	}
}

func TestPlaceConfigReadForImport(t *testing.T) {
	f := &fakePlaceConfig{store: map[string]opencloud.PlaceConfig{
		"1/2": {DisplayName: strp("Lobby"), Description: strp(""), ServerSize: i64p(40)},
	}}
	attrs, err := PlaceConfig(f).Read(context.Background(), "1/2")
	if err != nil {
		t.Fatal(err)
	}
	if attrs["universe_id"].AsString() != "1" || attrs["place_id"].AsString() != "2" ||
		attrs["display_name"].AsString() != "Lobby" || attrs["description"].AsString() != "" {
		t.Fatalf("attrs = %+v", attrs)
	}
	if _, err := PlaceConfig(f).Read(context.Background(), "bad"); err == nil {
		t.Fatal("expected an invalid-id error")
	}
}

func TestPlaceConfigReadMapsNotFound(t *testing.T) {
	r := PlaceConfig(&fakePlaceConfig{getErr: &opencloud.APIError{Status: 404}})
	if _, err := r.Read(context.Background(), "1/2"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want provider.ErrNotFound, got %v", err)
	}
}
