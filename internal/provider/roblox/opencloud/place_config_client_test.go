package opencloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlaceConfigGetAndUpdate(t *testing.T) {
	var gotPath, gotMask, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method == http.MethodPatch {
			gotMask = r.URL.Query().Get("updateMask")
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
		}
		io.WriteString(w, `{"path":"universes/1/places/2","displayName":"Lobby","description":"","serverSize":40}`)
	}))
	defer srv.Close()

	pc := NewPlaceConfigClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))

	got, err := pc.Get(context.Background(), "1", "2")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/cloud/v2/universes/1/places/2" {
		t.Fatalf("path = %q", gotPath)
	}
	if got.DisplayName == nil || *got.DisplayName != "Lobby" || got.ServerSize == nil || *got.ServerSize != 40 {
		t.Fatalf("get: %+v", got)
	}
	if got.Description == nil || *got.Description != "" {
		t.Fatalf("an empty description must decode as a non-nil empty string: %+v", got)
	}

	size := int64(12)
	if _, err := pc.Update(context.Background(), "1", "2", PlaceConfig{ServerSize: &size}, []string{"serverSize"}); err != nil {
		t.Fatal(err)
	}
	if gotMask != "serverSize" {
		t.Fatalf("updateMask = %q", gotMask)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body["serverSize"] != float64(12) {
		t.Fatalf("body = %s", gotBody)
	}
}
