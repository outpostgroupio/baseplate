package opencloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUniverseGetAndUpdate(t *testing.T) {
	var gotPatchMask string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			gotPatchMask = r.URL.Query().Get("updateMask")
		}
		vce := true
		json.NewEncoder(w).Encode(universeWire{Path: "universes/123", VoiceChatEnabled: &vce})
	}))
	defer srv.Close()

	uc := NewUniverseClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))

	got, err := uc.Get(context.Background(), "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "123" || got.VoiceChatEnabled == nil || !*got.VoiceChatEnabled {
		t.Fatalf("get: %+v", got)
	}

	yes := true
	if _, err := uc.Update(context.Background(), Universe{ID: "123", VoiceChatEnabled: &yes}, []string{"voiceChatEnabled"}); err != nil {
		t.Fatal(err)
	}
	if gotPatchMask != "voiceChatEnabled" {
		t.Fatalf("updateMask = %q", gotPatchMask)
	}
}

func newTestUniverseClient(h http.HandlerFunc) (UniverseClient, func()) {
	srv := httptest.NewServer(h)
	return NewUniverseClient(New("k", srv.URL, WithSleeper(func(time.Duration) {}))), srv.Close
}

func TestUniverseGetDecodesExtras(t *testing.T) {
	uc, done := newTestUniverseClient(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"path":"universes/1","visibility":"PUBLIC","privateServerPriceRobux":0,
			"discordSocialLink":{"title":"Join","uri":"https://discord.gg/x"}}`)
	})
	defer done()

	got, err := uc.Get(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Visibility != "PUBLIC" {
		t.Fatalf("visibility = %q", got.Visibility)
	}
	if got.PrivateServerPriceRobux == nil || *got.PrivateServerPriceRobux != 0 {
		t.Fatalf("price = %v", got.PrivateServerPriceRobux)
	}
	if l, ok := got.SocialLinks["discordSocialLink"]; !ok || l.Title != "Join" || l.URI != "https://discord.gg/x" {
		t.Fatalf("links = %+v", got.SocialLinks)
	}
	if len(got.SocialLinks) != 1 {
		t.Fatalf("unset links must be absent, got %+v", got.SocialLinks)
	}
}

func TestUniverseUpdateBodyOmitsClearedFields(t *testing.T) {
	var body map[string]json.RawMessage
	var mask string
	uc, done := newTestUniverseClient(func(w http.ResponseWriter, r *http.Request) {
		mask = r.URL.Query().Get("updateMask")
		body = nil
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"path":"universes/1"}`)
	})
	defer done()

	// Price nil + masked = disable private servers; Roblox 400s on an explicit
	// JSON null, so the field must be omitted. Links: a full link, a sub-path
	// (uri-only) link, and a cleared (masked, omitted) link.
	u := Universe{ID: "1", SocialLinks: map[string]SocialLink{
		"discordSocialLink": {Title: "Join", URI: "https://discord.gg/x"},
		"twitchSocialLink":  {URI: "https://twitch.tv/x"},
	}}
	m := []string{"discordSocialLink", "privateServerPriceRobux", "twitchSocialLink.uri", "youtubeSocialLink"}
	if _, err := uc.Update(context.Background(), u, m); err != nil {
		t.Fatal(err)
	}
	if mask != strings.Join(m, ",") {
		t.Fatalf("mask = %q", mask)
	}
	if _, ok := body["privateServerPriceRobux"]; ok {
		t.Fatalf("price must be omitted, body = %v", body)
	}
	if _, ok := body["youtubeSocialLink"]; ok {
		t.Fatalf("cleared link must be omitted, body = %v", body)
	}
	if got := string(body["discordSocialLink"]); got != `{"title":"Join","uri":"https://discord.gg/x"}` {
		t.Fatalf("discord = %s", got)
	}
	if got := string(body["twitchSocialLink"]); got != `{"uri":"https://twitch.tv/x"}` {
		t.Fatalf("twitch = %s", got)
	}

	price := int64(25)
	if _, err := uc.Update(context.Background(), Universe{ID: "1", PrivateServerPriceRobux: &price}, []string{"privateServerPriceRobux"}); err != nil {
		t.Fatal(err)
	}
	if got := string(body["privateServerPriceRobux"]); got != "25" {
		t.Fatalf("price = %s", got)
	}
}

func TestUniverseSetPublic(t *testing.T) {
	var paths []string
	uc, done := newTestUniverseClient(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		paths = append(paths, r.URL.Path)
	})
	defer done()

	if err := uc.SetPublic(context.Background(), "1", true); err != nil {
		t.Fatal(err)
	}
	if err := uc.SetPublic(context.Background(), "1", false); err != nil {
		t.Fatal(err)
	}
	want := "/legacy-develop/v1/universes/1/activate /legacy-develop/v1/universes/1/deactivate"
	if got := strings.Join(paths, " "); got != want {
		t.Fatalf("paths = %v", got)
	}
}

func TestUniverseSetPublicSurfacesLegacyError(t *testing.T) {
	uc, done := newTestUniverseClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"errors":[{"code":0,"message":"Scope not authorized."}]}`)
	})
	defer done()

	err := uc.SetPublic(context.Background(), "1", true)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden || apiErr.Message != "Scope not authorized." {
		t.Fatalf("err = %#v", err)
	}
}
