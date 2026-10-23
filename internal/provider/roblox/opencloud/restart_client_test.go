package opencloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func restartServer(t *testing.T, status int, resp string, got *map[string]any, gotPath *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected method "+r.Method, 405)
			return
		}
		*gotPath = r.URL.Path
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, got); err != nil {
			t.Errorf("body is not JSON: %q", b)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
}

func TestRestartServersBleedOffAndPlaces(t *testing.T) {
	var got map[string]any
	var path string
	srv := restartServer(t, 200, `{}`, &got, &path)
	defer srv.Close()

	rc := NewServerRestartClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	err := rc.RestartServers(context.Background(), RestartServersRequest{
		UniverseID:       "100",
		PlaceIDs:         []int64{200, 300},
		CloseAllVersions: true,
		BleedOffMinutes:  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/cloud/v2/universes/100:restartServers" {
		t.Fatalf("path = %q", path)
	}
	if got["closeAllVersions"] != true || got["bleedOffServers"] != true || got["bleedOffDurationMinutes"] != float64(5) {
		t.Fatalf("body = %v", got)
	}
	ids, _ := got["placeIds"].([]any)
	if len(ids) != 2 || ids[0] != float64(200) || ids[1] != float64(300) {
		t.Fatalf("placeIds = %v", got["placeIds"])
	}
}

func TestRestartServersImmediateOmitsDuration(t *testing.T) {
	var got map[string]any
	var path string
	srv := restartServer(t, 200, `{}`, &got, &path)
	defer srv.Close()

	rc := NewServerRestartClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	if err := rc.RestartServers(context.Background(), RestartServersRequest{UniverseID: "100"}); err != nil {
		t.Fatal(err)
	}
	// closeAllVersions/bleedOffServers are always sent explicitly so the
	// request never depends on server-side defaults; no duration, no places.
	if got["closeAllVersions"] != false || got["bleedOffServers"] != false {
		t.Fatalf("body = %v", got)
	}
	if _, ok := got["bleedOffDurationMinutes"]; ok {
		t.Fatalf("bleedOffDurationMinutes must be omitted for an immediate restart: %v", got)
	}
	if _, ok := got["placeIds"]; ok {
		t.Fatalf("placeIds must be omitted when no places are given: %v", got)
	}
}

func TestRestartServersRejectsOutOfRangeBleedOff(t *testing.T) {
	rc := NewServerRestartClient(New("k", "http://unused.invalid"))
	for _, m := range []int{-1, 61} {
		err := rc.RestartServers(context.Background(), RestartServersRequest{UniverseID: "100", BleedOffMinutes: m})
		if err == nil || !strings.Contains(err.Error(), "between 1 and 60") {
			t.Fatalf("bleed-off %d: want a range error, got %v", m, err)
		}
	}
}

func TestRestartServersRequiresUniverse(t *testing.T) {
	rc := NewServerRestartClient(New("k", "http://unused.invalid"))
	if err := rc.RestartServers(context.Background(), RestartServersRequest{}); err == nil {
		t.Fatal("want an error for a missing universe id")
	}
}

func TestRestartServersSurfacesAPIError(t *testing.T) {
	var got map[string]any
	var path string
	srv := restartServer(t, 404, `{"code":"NOT_FOUND","message":"The desired Place was not found."}`, &got, &path)
	defer srv.Close()

	rc := NewServerRestartClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	err := rc.RestartServers(context.Background(), RestartServersRequest{UniverseID: "100", PlaceIDs: []int64{1}})
	if err == nil || !strings.Contains(err.Error(), "Place was not found") {
		t.Fatalf("want the API error surfaced, got %v", err)
	}
}
