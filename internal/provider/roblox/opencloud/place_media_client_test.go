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

func TestPlaceMediaGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/assets/9" || r.URL.Query().Get("readMask") != "assetType,icon,previews" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), 404)
			return
		}
		// Live shape: an unset alt text is absent.
		w.Write([]byte(`{"assetType":"Place","icon":"assets/5","previews":[{"asset":"assets/6","altText":"a"},{"asset":"assets/7"}]}`))
	}))
	defer srv.Close()

	m, err := NewPlaceMediaClient(New("k", srv.URL)).Get(context.Background(), "9")
	if err != nil {
		t.Fatal(err)
	}
	if m.AssetType != "Place" || m.Icon != "5" || len(m.Thumbnails) != 2 ||
		m.Thumbnails[0] != (PlaceThumbnail{AssetID: "6", AltText: "a"}) || m.Thumbnails[1] != (PlaceThumbnail{AssetID: "7"}) {
		t.Fatalf("got %+v", m)
	}
}

func TestPlaceMediaGetUnset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"assetType":"Place"}`)) // live: no icon and no previews are absent
	}))
	defer srv.Close()

	m, err := NewPlaceMediaClient(New("k", srv.URL)).Get(context.Background(), "9")
	if err != nil {
		t.Fatal(err)
	}
	if m.Icon != "" || m.Thumbnails == nil || len(m.Thumbnails) != 0 {
		t.Fatalf("got %+v", m)
	}
}

func TestPlaceMediaUpdateSendsMaskedFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    PlaceMedia
		mask []string
		want string
	}{
		{"both", PlaceMedia{Icon: "5", Thumbnails: []PlaceThumbnail{{AssetID: "6", AltText: "a"}, {AssetID: "7"}}}, []string{"icon", "previews"},
			`{"assetId":"9","icon":"assets/5","previews":[{"asset":"assets/6","altText":"a"},{"asset":"assets/7"}]}`},
		{"clear both", PlaceMedia{}, []string{"icon", "previews"}, `{"assetId":"9","icon":"","previews":[]}`},
		{"icon only", PlaceMedia{Icon: "5", Thumbnails: []PlaceThumbnail{{AssetID: "6"}}}, []string{"icon"}, `{"assetId":"9","icon":"assets/5"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotMask, gotReq string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPatch && r.URL.Path == "/v1/assets/9":
					gotMask = r.URL.Query().Get("updateMask")
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Errorf("parse multipart: %v", err)
					}
					gotReq = r.FormValue("request")
					json.NewEncoder(w).Encode(map[string]any{"path": "operations/op1", "done": false})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/op1":
					json.NewEncoder(w).Encode(map[string]any{"path": "operations/op1", "done": true, "response": map[string]any{"assetId": "9"}})
				default:
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
				}
			}))
			defer srv.Close()

			c := NewPlaceMediaClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
			if err := c.Update(context.Background(), "9", tc.m, tc.mask); err != nil {
				t.Fatal(err)
			}
			if gotMask != strings.Join(tc.mask, ",") {
				t.Errorf("updateMask = %q", gotMask)
			}
			if gotReq != tc.want {
				t.Errorf("request = %s\nwant      %s", gotReq, tc.want)
			}
		})
	}
}

// A failed asset PATCH puts the real error in metadata.errors and a generic one
// in error (live: "Icon asset ID 1 is not found.").
func TestPlaceMediaUpdateReportsOperationMetadataError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			json.NewEncoder(w).Encode(map[string]any{"path": "operations/op1", "done": false})
		default:
			w.Write([]byte(`{"path":"operations/op1","done":true,` +
				`"metadata":{"errors":[{"fieldMask":{"paths":["icon"]},"error":{"code":"InvalidArgument","message":"Icon asset ID 1 is not found.","details":[]}}]},` +
				`"error":{"code":"Unknown","message":"Error occured during operation, check metadata for specifics.","details":[]}}`))
		}
	}))
	defer srv.Close()

	c := NewPlaceMediaClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	err := c.Update(context.Background(), "9", PlaceMedia{Icon: "1"}, []string{"icon"})
	if err == nil || err.Error() != "Roblox API operation failed: Icon asset ID 1 is not found." {
		t.Fatalf("err = %v", err)
	}
}
