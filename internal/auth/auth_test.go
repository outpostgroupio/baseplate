package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLoginServer is a state server whose sign-in approves at once: its
// authorization endpoint redirects straight back with a code, and its token
// endpoint checks the PKCE verifier.
func fakeLoginServer(t *testing.T) *httptest.Server {
	t.Helper()
	var challenge, redirect string
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc(DiscoveryPath, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"login.v1": map[string]any{
			"client": "baseplate-cli", "grant_types": []string{"authz_code"},
			"authz": "/oauth/authorize", "token": "/oauth/token", "ports": []int{10000, 10010},
		}})
	})
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "baseplate-cli" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		challenge, redirect = q.Get("code_challenge"), q.Get("redirect_uri")
		http.Redirect(w, r, redirect+"?code=the-code&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "the-code" || r.Form.Get("redirect_uri") != redirect ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "issued-token", "token_type": "bearer"})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// visit plays the browser: it follows the authorization redirects to the
// CLI's localhost listener.
func visit(t *testing.T) func(string) error {
	return func(u string) error {
		go func() {
			resp, err := http.Get(u)
			if err != nil {
				t.Errorf("browser: %v", err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
		return nil
	}
}

func hostOf(t *testing.T, srv *httptest.Server) string {
	u, _ := url.Parse(srv.URL)
	// httptest listens on 127.0.0.1, which Origin serves over plain http.
	return u.Host
}

func TestLoginRunsTheAuthorizationCodeFlow(t *testing.T) {
	srv := fakeLoginServer(t)
	var out strings.Builder
	tok, err := Login(context.Background(), hostOf(t, srv), LoginOptions{OpenBrowser: visit(t), Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "issued-token" {
		t.Fatalf("token = %q", tok)
	}
	if !strings.Contains(out.String(), "/oauth/authorize?") {
		t.Fatalf("the authorization URL should be printed for headless use:\n%s", out.String())
	}
}

func TestLoginRefusesServerWithoutLoginV1(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := Login(context.Background(), hostOf(t, srv), LoginOptions{Out: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "doesn't support baseplate login") {
		t.Fatalf("want a not-supported error, got %v", err)
	}
}

func TestStoreTokenRoundTripsWithPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds", "credentials.json")
	t.Setenv(CredentialsEnv, path)
	if err := StoreToken("state.example.com", "abc"); err != nil {
		t.Fatal(err)
	}
	if got, _ := StoredToken("state.example.com"); got != "abc" {
		t.Fatalf("stored token = %q", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %v, want 0600", fi.Mode().Perm())
	}
	if err := StoreToken("state.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := StoredToken("state.example.com"); got != "" {
		t.Fatalf("logout should remove the token, got %q", got)
	}
}

func TestTokenPrefersEnvThenStored(t *testing.T) {
	t.Setenv(CredentialsEnv, filepath.Join(t.TempDir(), "credentials.json"))
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	StoreToken("state.example.com", "stored")
	t.Setenv(TokenEnv, "from-env")
	if got, _ := Token(context.Background(), http.DefaultClient, "state.example.com"); got != "from-env" {
		t.Fatalf("env token should win, got %q", got)
	}
	t.Setenv(TokenEnv, "")
	if got, _ := Token(context.Background(), http.DefaultClient, "state.example.com"); got != "stored" {
		t.Fatalf("stored token = %q", got)
	}
}

func TestTokenUsesGitHubActionsOIDCWhenServerAcceptsIt(t *testing.T) {
	t.Setenv(TokenEnv, "")
	t.Setenv(CredentialsEnv, filepath.Join(t.TempDir(), "credentials.json"))
	var audience string
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		audience = r.URL.Query().Get("audience")
		json.NewEncoder(w).Encode(map[string]string{"value": "oidc-jwt"})
	}))
	defer runtime.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", runtime.URL+"/token?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runtime-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"oidc.v1": map[string]any{}})
	}))
	defer server.Close()
	host := hostOf(t, server)
	got, err := Token(context.Background(), http.DefaultClient, host)
	if err != nil {
		t.Fatal(err)
	}
	if got != "oidc-jwt" {
		t.Fatalf("token = %q, want the OIDC token", got)
	}
	if audience != "http://"+host {
		t.Fatalf("audience = %q, want the server's origin", audience)
	}
}

func TestOrigin(t *testing.T) {
	for host, want := range map[string]string{
		"state.example.com":      "https://state.example.com",
		"state.example.com:8443": "https://state.example.com:8443",
		"localhost:8787":         "http://localhost:8787",
		"127.0.0.1":              "http://127.0.0.1",
		"[::1]:8787":             "http://[::1]:8787",
	} {
		if got := Origin(host); got != want {
			t.Errorf("Origin(%q) = %q, want %q", host, got, want)
		}
	}
}
