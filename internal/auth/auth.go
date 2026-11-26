// Package auth finds the token Baseplate sends to a state server, and signs in
// to one with `baseplate login`.
//
// A state server (e.g. the reference Cloudflare Worker in workers/state)
// describes how to sign in at /.well-known/baseplate.json. Its "login.v1"
// entry is the same OAuth 2.0 authorization-code + PKCE flow that
// `terraform login` uses: the CLI listens on a localhost port, opens the
// browser at the server's authorization endpoint, and exchanges the code it
// gets back for a token. Its "oidc.v1" entry says the server accepts GitHub
// Actions OIDC tokens, which CI uses instead of a stored secret.
//
// The protocol is documented in docs/state-server-protocol.md.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TokenEnv is the environment variable that, when set, is sent as the state
// server token, ahead of any stored credentials.
const TokenEnv = "BASEPLATE_STATE_TOKEN"

// CredentialsEnv overrides the credentials file location.
const CredentialsEnv = "BASEPLATE_CREDENTIALS_FILE"

// DiscoveryPath is where a state server describes its sign-in methods.
const DiscoveryPath = "/.well-known/baseplate.json"

// Discovery is a state server's /.well-known/baseplate.json.
type Discovery struct {
	Login *LoginV1 `json:"login.v1,omitempty"`
	OIDC  *OIDCV1  `json:"oidc.v1,omitempty"`
	// Outputs, when present, says GET <state address>?outputs returns only
	// the state's output values, for baseplate_remote_state. A server can let
	// an identity read outputs without reading the whole state.
	Outputs *OutputsV1 `json:"outputs.v1,omitempty"`
}

// OutputsV1 has no fields yet.
type OutputsV1 struct{}

// LoginV1 describes an OAuth 2.0 authorization-code flow with PKCE, with the
// same fields as Terraform's login.v1 service.
type LoginV1 struct {
	Client     string   `json:"client"`
	GrantTypes []string `json:"grant_types"`
	Authz      string   `json:"authz"`
	Token      string   `json:"token"`
	// Ports is the inclusive range of localhost ports the server accepts in
	// the redirect URI.
	Ports [2]int `json:"ports"`
}

// OIDCV1 says the server accepts GitHub Actions OIDC tokens whose audience is
// the server's origin.
type OIDCV1 struct {
	Issuers []string `json:"issuers,omitempty"`
}

// Origin returns the scheme://host[:port] a state server is reached at.
// Plain http is only used for loopback hosts (local development).
func Origin(host string) string {
	h := host
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.HasSuffix(h, "]") {
		h = h[:i]
	}
	h = strings.Trim(h, "[]")
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return "http://" + host
	}
	return "https://" + host
}

// HostOf returns the host[:port] of a state URL.
func HostOf(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid state address %q", address)
	}
	return u.Host, nil
}

// Discover fetches the server's discovery document.
func Discover(ctx context.Context, client *http.Client, host string) (*Discovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Origin(host)+DiscoveryPath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &Discovery{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s%s: %s: %s", host, DiscoveryPath, resp.Status, strings.TrimSpace(string(b)))
	}
	var d Discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("%s%s: %w", host, DiscoveryPath, err)
	}
	return &d, nil
}

// credentialsFile is the on-disk shape, like Terraform's credentials.tfrc.json.
type credentialsFile struct {
	Credentials map[string]struct {
		Token string `json:"token"`
	} `json:"credentials"`
}

// CredentialsPath returns where `baseplate login` stores tokens.
func CredentialsPath() (string, error) {
	if p := os.Getenv(CredentialsEnv); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "baseplate", "credentials.json"), nil
}

func readCredentials() (*credentialsFile, string, error) {
	path, err := CredentialsPath()
	if err != nil {
		return nil, "", err
	}
	cf := &credentialsFile{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cf, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	if err := json.Unmarshal(b, cf); err != nil {
		return nil, path, fmt.Errorf("%s: %w", path, err)
	}
	return cf, path, nil
}

// StoredToken returns the token `baseplate login` stored for host, or "".
func StoredToken(host string) (string, error) {
	cf, _, err := readCredentials()
	if err != nil {
		return "", err
	}
	return cf.Credentials[host].Token, nil
}

// StoreToken saves (or, with an empty token, removes) the token for host. The
// file is written with mode 0600.
func StoreToken(host, token string) error {
	cf, path, err := readCredentials()
	if err != nil {
		return err
	}
	if cf.Credentials == nil {
		cf.Credentials = map[string]struct {
			Token string `json:"token"`
		}{}
	}
	if token == "" {
		delete(cf.Credentials, host)
	} else {
		cf.Credentials[host] = struct {
			Token string `json:"token"`
		}{Token: token}
	}
	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Token returns the token to send to the state server at host, in order of
// precedence: $BASEPLATE_STATE_TOKEN; a GitHub Actions OIDC token, when
// running in a job with `id-token: write` against a server that accepts them;
// the token stored by `baseplate login`. It returns "" when there is none.
func Token(ctx context.Context, client *http.Client, host string) (string, error) {
	if t := os.Getenv(TokenEnv); t != "" {
		return t, nil
	}
	if InGitHubActions() {
		d, err := Discover(ctx, client, host)
		if err != nil {
			return "", err
		}
		if d.OIDC != nil {
			return GitHubActionsToken(ctx, client, Origin(host))
		}
	}
	return StoredToken(host)
}

// InGitHubActions reports whether this is a GitHub Actions job that may
// request an OIDC token (the workflow grants `id-token: write`).
func InGitHubActions() bool {
	return os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") != "" && os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN") != ""
}

// GitHubActionsToken requests an OIDC token for audience from the GitHub
// Actions runtime. The audience is always the state server's own origin, so
// the token can't be replayed against another service.
func GitHubActionsToken(ctx context.Context, client *http.Client, audience string) (string, error) {
	u, err := url.Parse(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"))
	if err != nil {
		return "", fmt.Errorf("github actions oidc: invalid ACTIONS_ID_TOKEN_REQUEST_URL: %w", err)
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github actions oidc: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("github actions oidc: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Value == "" {
		return "", fmt.Errorf("github actions oidc: unexpected response")
	}
	return out.Value, nil
}
