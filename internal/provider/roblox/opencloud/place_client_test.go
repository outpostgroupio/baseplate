package opencloud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlacePublish(t *testing.T) {
	var gotBody []byte
	var gotCT, gotVersionType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/universes/v1/100/places/200/versions" {
			gotBody, _ = io.ReadAll(r.Body)
			gotCT = r.Header.Get("Content-Type")
			gotVersionType = r.URL.Query().Get("versionType")
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, `{"versionNumber":7}`)
			return
		}
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
	}))
	defer srv.Close()

	pc := NewPlaceClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	v, err := pc.Publish(context.Background(), "100", "200", "Published", "application/xml", []byte("<roblox/>"))
	if err != nil {
		t.Fatal(err)
	}
	if v != 7 {
		t.Fatalf("version = %d, want 7", v)
	}
	if string(gotBody) != "<roblox/>" {
		t.Fatalf("body = %q", gotBody)
	}
	if gotCT != "application/xml" {
		t.Fatalf("content-type = %q, want application/xml", gotCT)
	}
	if gotVersionType != "Published" {
		t.Fatalf("versionType = %q, want Published", gotVersionType)
	}
}
