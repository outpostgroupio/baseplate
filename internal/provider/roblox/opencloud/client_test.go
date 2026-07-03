package opencloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoSetsAuthAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"name": "hello"})
	}))
	defer srv.Close()

	c := New("secret", srv.URL, WithSleeper(func(time.Duration) {}))
	var out struct {
		Name string `json:"name"`
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/v2/thing", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "hello" {
		t.Fatalf("got %q", out.Name)
	}
}

func TestDoRetriesOn429ThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(429)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer srv.Close()

	c := New("k", srv.URL, WithSleeper(func(time.Duration) {}))
	var out map[string]string
	if _, err := c.Do(context.Background(), http.MethodGet, "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestDoReturnsAPIErrorOnPersistent4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"code": "INSUFFICIENT_SCOPE", "message": "missing scope"})
	}))
	defer srv.Close()

	c := New("k", srv.URL, WithSleeper(func(time.Duration) {}))
	_, err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.Status != 403 || apiErr.Message != "missing scope" {
		t.Fatalf("got %+v", apiErr)
	}
}

func TestPollOperationWaitsForDone(t *testing.T) {
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls < 2 {
			json.NewEncoder(w).Encode(map[string]any{"path": "/ops/1", "done": false})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"path": "/ops/1", "done": true,
			"response": map[string]string{"assetId": "123"}})
	}))
	defer srv.Close()

	c := New("k", srv.URL, WithSleeper(func(time.Duration) {}))
	var out struct {
		AssetID string `json:"assetId"`
	}
	if err := c.PollOperation(context.Background(), "/ops/1", &out); err != nil {
		t.Fatal(err)
	}
	if out.AssetID != "123" {
		t.Fatalf("got %q", out.AssetID)
	}
}
