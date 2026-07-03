package opencloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
