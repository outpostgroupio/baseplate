package roblox

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

type fakeRestartClient struct {
	got []opencloud.RestartServersRequest
}

func (f *fakeRestartClient) RestartServers(_ context.Context, req opencloud.RestartServersRequest) error {
	f.got = append(f.got, req)
	return nil
}

func TestProviderImplementsServerRestarter(t *testing.T) {
	p, err := New("x")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := any(p).(provider.ServerRestarter); !ok {
		t.Fatal("the Roblox provider must implement provider.ServerRestarter")
	}
}

func TestRestartServersTranslatesRequest(t *testing.T) {
	f := &fakeRestartClient{}
	p := &Provider{restarter: f}
	err := p.RestartServers(context.Background(), provider.RestartRequest{
		UniverseID:  "100",
		PlaceIDs:    []string{"200", "300"},
		AllVersions: true,
		BleedOff:    10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := opencloud.RestartServersRequest{UniverseID: "100", PlaceIDs: []int64{200, 300}, CloseAllVersions: true, BleedOffMinutes: 10}
	if len(f.got) != 1 || !reflect.DeepEqual(f.got[0], want) {
		t.Fatalf("got %+v, want %+v", f.got, want)
	}
}

func TestRestartServersRejectsBadInput(t *testing.T) {
	for name, req := range map[string]provider.RestartRequest{
		"fractional minutes": {UniverseID: "1", BleedOff: 90 * time.Second},
		"sub-minute":         {UniverseID: "1", BleedOff: 30 * time.Second},
		"over max":           {UniverseID: "1", BleedOff: 61 * time.Minute},
		"non-numeric place":  {UniverseID: "1", PlaceIDs: []string{"abc"}},
	} {
		f := &fakeRestartClient{}
		p := &Provider{restarter: f}
		if err := p.RestartServers(context.Background(), req); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if len(f.got) != 0 {
			t.Errorf("%s: no request may be sent on invalid input", name)
		}
	}
	err := (&Provider{}).ValidateRestart(provider.RestartRequest{BleedOff: 90 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "whole number of minutes") {
		t.Fatalf("want a whole-minutes error, got %v", err)
	}
}
