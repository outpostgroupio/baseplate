package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LoginOptions configures Login.
type LoginOptions struct {
	Client *http.Client
	// OpenBrowser opens url in the user's browser. It may fail (e.g. on a
	// headless machine); the URL is also printed, so the user can open it.
	OpenBrowser func(url string) error
	// Out receives the instructions for the user.
	Out io.Writer
	// Timeout bounds the wait for the browser to come back (default 5m).
	Timeout time.Duration
}

// Login signs in to the state server at host with its login.v1 flow and
// returns the token it issues. It doesn't store the token.
func Login(ctx context.Context, host string, opts LoginOptions) (string, error) {
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Minute
	}
	d, err := Discover(ctx, opts.Client, host)
	if err != nil {
		return "", err
	}
	lv := d.Login
	if lv == nil {
		return "", fmt.Errorf("%s doesn't support baseplate login (no login.v1 in %s)", host, DiscoveryPath)
	}
	if !contains(lv.GrantTypes, "authz_code") {
		return "", fmt.Errorf("%s: login.v1 doesn't offer the authz_code grant", host)
	}
	origin := Origin(host)
	authzURL, err := resolve(origin, lv.Authz)
	if err != nil {
		return "", err
	}
	tokenURL, err := resolve(origin, lv.Token)
	if err != nil {
		return "", err
	}
	// Login sends a code verifier to tokenURL, so it must be the same server.
	if sameOrigin(tokenURL, origin) != nil {
		return "", fmt.Errorf("%s: login.v1 token endpoint %s is not on %s", host, tokenURL, origin)
	}

	ln, port, err := listen(lv.Ports)
	if err != nil {
		return "", err
	}
	defer ln.Close()
	// A loopback IP literal, not "localhost", so the browser reaches the
	// listener even where localhost resolves to ::1 first (RFC 8252 §7.3).
	redirect := fmt.Sprintf("http://127.0.0.1:%d/login", port)
	verifier := randomString(32)
	state := randomString(16)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {lv.Client},
		"redirect_uri":          {redirect},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	authz := authzURL + sep(authzURL) + q.Encode()

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("state") != state:
			res.err = errors.New("the sign-in response didn't match this login attempt (state mismatch); run baseplate login again")
		case q.Get("error") != "":
			res.err = fmt.Errorf("sign-in failed: %s %s", q.Get("error"), q.Get("error_description"))
		case q.Get("code") == "":
			res.err = errors.New("sign-in response had no authorization code")
		default:
			res.code = q.Get("code")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<!doctype html><title>Baseplate</title><p>%s</p>", html.EscapeString(res.err.Error()))
		} else {
			fmt.Fprint(w, "<!doctype html><title>Baseplate</title><p>Signed in. You can close this tab and return to the terminal.</p>")
		}
		select {
		case done <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Fprintf(opts.Out, "Opening your browser to sign in to %s. If it doesn't open, visit:\n\n    %s\n\n", host, authz)
	if opts.OpenBrowser != nil {
		_ = opts.OpenBrowser(authz)
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return "", errors.New("timed out waiting for the browser sign-in")
	}
	if res.err != nil {
		return "", res.err
	}
	return exchange(ctx, opts.Client, tokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {lv.Client},
		"code":          {res.code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirect},
	})
}

func exchange(ctx context.Context, client *http.Client, tokenURL string, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(b, &out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		if out.Error != "" {
			return "", fmt.Errorf("token exchange failed: %s %s", out.Error, out.Description)
		}
		return "", fmt.Errorf("token exchange failed: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return out.AccessToken, nil
}

// listen binds the first free localhost port in the server's range.
func listen(ports [2]int) (net.Listener, int, error) {
	lo, hi := ports[0], ports[1]
	if lo <= 0 || hi < lo {
		return nil, 0, fmt.Errorf("login.v1: invalid ports %v", ports)
	}
	for p := lo; p <= hi; p++ {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			return ln, p, nil
		}
	}
	return nil, 0, fmt.Errorf("no free localhost port in %d-%d for the sign-in redirect", lo, hi)
}

func resolve(origin, ref string) (string, error) {
	base, err := url.Parse(origin + "/")
	if err != nil {
		return "", err
	}
	u, err := base.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("login.v1: invalid endpoint %q: %w", ref, err)
	}
	return u.String(), nil
}

func sameOrigin(raw, origin string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme+"://"+u.Host != origin {
		return errors.New("different origin")
	}
	return nil
}

func sep(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
