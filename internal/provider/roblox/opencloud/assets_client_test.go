package opencloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAssetCreateAndAwait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/assets":
			json.NewEncoder(w).Encode(map[string]any{"path": "operations/op1", "done": false})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/op1":
			json.NewEncoder(w).Encode(map[string]any{"path": "operations/op1", "done": true,
				"response": map[string]any{"assetId": "555", "path": "assets/555", "displayName": "gem"}})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	ac := NewAssetClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	a, err := ac.CreateAndAwait(context.Background(),
		AssetCreate{AssetType: "Image", DisplayName: "gem", Description: "d", CreatorUserID: 42},
		"i.png", "image/png", []byte("PNGDATA"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "555" {
		t.Fatalf("asset id = %q, want 555", a.ID)
	}
	if a.DisplayName != "gem" {
		t.Fatalf("display name = %q", a.DisplayName)
	}
}

func TestAssetGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/assets/555" {
			json.NewEncoder(w).Encode(map[string]any{"assetId": "555", "path": "assets/555",
				"displayName": "gem", "description": "d"})
			return
		}
		http.Error(w, "unexpected", 404)
	}))
	defer srv.Close()

	ac := NewAssetClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	a, err := ac.Get(context.Background(), "555")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "555" || a.Description != "d" {
		t.Fatalf("got %+v", a)
	}
}

func TestAssetUpdateIsMultipartWithMaskAndAwaitsOperation(t *testing.T) {
	var gotMask, gotCT string
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/assets/555":
			gotMask = r.URL.Query().Get("updateMask")
			gotCT = r.Header.Get("Content-Type")
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			if err := json.Unmarshal([]byte(r.FormValue("request")), &gotReq); err != nil {
				t.Errorf("request part: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{"path": "operations/op2", "done": false})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/op2":
			json.NewEncoder(w).Encode(map[string]any{"path": "operations/op2", "done": true,
				"response": map[string]any{"assetId": "555", "displayName": "new", "description": ""}})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	ac := NewAssetClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	a, err := ac.Update(context.Background(), Asset{ID: "555", DisplayName: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if gotMask != "displayName,description" {
		t.Fatalf("updateMask = %q", gotMask)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data") {
		t.Fatalf("content type = %q", gotCT)
	}
	if gotReq["assetId"] != "555" || gotReq["displayName"] != "new" {
		t.Fatalf("request part = %+v", gotReq)
	}
	if d, ok := gotReq["description"]; !ok || d != "" {
		t.Fatalf("an empty description must still be sent (mask clears it): %+v", gotReq)
	}
	if a.DisplayName != "new" {
		t.Fatalf("got %+v", a)
	}
}
