package resources

import (
	"context"
	"errors"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/roblox/opencloud"
)

type fakeUniverse struct {
	store   map[string]opencloud.Universe
	updates []opencloud.Universe
	masks   [][]string
	getErr  error
}

func (f *fakeUniverse) Get(_ context.Context, id string) (opencloud.Universe, error) {
	if f.getErr != nil {
		return opencloud.Universe{}, f.getErr
	}
	return f.store[id], nil
}
func (f *fakeUniverse) Update(_ context.Context, u opencloud.Universe, mask []string) (opencloud.Universe, error) {
	f.updates = append(f.updates, u)
	f.masks = append(f.masks, mask)
	if f.store == nil {
		f.store = map[string]opencloud.Universe{}
	}
	f.store[u.ID] = u
	return u, nil
}

func TestExperienceCreateAdoptsAndConfigures(t *testing.T) {
	f := &fakeUniverse{store: map[string]opencloud.Universe{}}
	e := Experience(f)

	id, attrs, err := e.Create(context.Background(), provider.Attributes{
		"universe_id":        cty.StringVal("123"),
		"voice_chat_enabled": cty.True,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "123" {
		t.Fatalf("id = %q", id)
	}
	if len(f.updates) != 1 || f.updates[0].VoiceChatEnabled == nil || !*f.updates[0].VoiceChatEnabled {
		t.Fatalf("expected an update setting voice chat, got %+v", f.updates)
	}
	if len(f.masks) != 1 || len(f.masks[0]) != 1 || f.masks[0][0] != "voiceChatEnabled" {
		t.Fatalf("mask = %+v", f.masks)
	}
	if attrs["voice_chat_enabled"].True() != true {
		t.Fatalf("attrs = %+v", attrs)
	}
}

func TestExperienceReadMapsNotFound(t *testing.T) {
	e := Experience(&fakeUniverse{getErr: &opencloud.APIError{Status: 404}})
	if _, err := e.Read(context.Background(), "123"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want provider.ErrNotFound, got %v", err)
	}
}

func TestExperienceReadPassesThroughOtherErrors(t *testing.T) {
	e := Experience(&fakeUniverse{getErr: &opencloud.APIError{Status: 500}})
	_, err := e.Read(context.Background(), "123")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("a 500 must not map to ErrNotFound, got %v", err)
	}
}
