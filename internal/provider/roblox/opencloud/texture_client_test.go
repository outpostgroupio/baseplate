package opencloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func textureServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/toolbox-service/v2/assets/777" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 500)
			return
		}
		if r.Header.Get("x-api-key") != "k" {
			http.Error(w, "missing key", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDecalTextureIDNumber(t *testing.T) {
	srv := textureServer(t, 200, `{"asset":{"id":777,"name":"d","assetTypeId":13,"textureId":778},"creator":{"id":1}}`)
	tc := NewTextureClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	got, err := tc.DecalTextureID(context.Background(), "777")
	if err != nil {
		t.Fatal(err)
	}
	if got != "778" {
		t.Fatalf("texture id = %q, want 778", got)
	}
}

// A 64-bit id may arrive as a JSON string (as assetId does on the assets API).
func TestDecalTextureIDString(t *testing.T) {
	srv := textureServer(t, 200, `{"asset":{"id":"777","textureId":"123456789012345678"}}`)
	tc := NewTextureClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	got, err := tc.DecalTextureID(context.Background(), "777")
	if err != nil {
		t.Fatal(err)
	}
	if got != "123456789012345678" {
		t.Fatalf("texture id = %q", got)
	}
}

// A large numeric id must not lose precision through float64.
func TestDecalTextureIDLargeNumber(t *testing.T) {
	srv := textureServer(t, 200, `{"asset":{"textureId":123456789012345678}}`)
	tc := NewTextureClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	got, err := tc.DecalTextureID(context.Background(), "777")
	if err != nil {
		t.Fatal(err)
	}
	if got != "123456789012345678" {
		t.Fatalf("texture id = %q", got)
	}
}

func TestDecalTextureIDMissing(t *testing.T) {
	for _, body := range []string{`{"asset":{"id":777}}`, `{"asset":{"textureId":0}}`, `{"asset":{"textureId":null}}`, `{}`} {
		srv := textureServer(t, 200, body)
		tc := NewTextureClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
		_, err := tc.DecalTextureID(context.Background(), "777")
		if !errors.Is(err, ErrNoTextureID) {
			t.Fatalf("%s: want ErrNoTextureID, got %v", body, err)
		}
	}
}

func TestDecalTextureIDErrors(t *testing.T) {
	srv := textureServer(t, 404, `{"type":"https://tools.ietf.org/html/rfc9110#section-15.5.5","title":"Not Found","status":404}`)
	tc := NewTextureClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	if _, err := tc.DecalTextureID(context.Background(), "777"); !IsNotFound(err) {
		t.Fatalf("want a 404 APIError, got %v", err)
	}

	srv = textureServer(t, 403, `{"errors":[{"code":0,"message":"Scope not authorized."}]}`)
	tc = NewTextureClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	_, err := tc.DecalTextureID(context.Background(), "777")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Message != "Scope not authorized." {
		t.Fatalf("want the 403 APIError, got %v", err)
	}
}
