package opencloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
	sleep   func(time.Duration)
	maxTry  int
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option     { return func(c *Client) { c.http = h } }
func WithSleeper(s func(time.Duration)) Option { return func(c *Client) { c.sleep = s } }

func New(apiKey, baseURL string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 60 * time.Second},
		sleep:   time.Sleep,
		maxTry:  5,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("Roblox API %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("Roblox API %d", e.Status)
}

// IsNotFound reports whether err is, or wraps, a Roblox API 404 response.
// It lets resources translate an out-of-band deletion into provider.ErrNotFound
// without the opencloud core depending on internal/provider.
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsNotArchivable reports whether err is a Roblox API 400 that rejected an
// archive because the asset's type cannot be archived (e.g. Image/Decal). Such
// assets have no Open Cloud delete path, so destroy unmanages them in place.
// The substring match is brittle but fails closed (an unrelated 400 propagates,
// so we never silently orphan-and-succeed on a different error).
// TODO: revisit if Roblox exposes a structured error code; pin with a contract test (Phase 4).
func IsNotArchivable(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(e.Message), "archivable")
}

func (c *Client) Do(ctx context.Context, method, path string, body, out any) (*http.Response, error) {
	var reqBody []byte
	contentType := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = b
		contentType = "application/json"
	}
	return c.send(ctx, method, path, contentType, reqBody, out)
}

// DoRaw sends body verbatim with the given Content-Type (e.g. a raw .rbxl place
// upload), reusing auth, retry/backoff, and error translation. A JSON response
// body is decoded into out when non-nil, even if served as text/plain.
func (c *Client) DoRaw(ctx context.Context, method, path, contentType string, body []byte, out any) (*http.Response, error) {
	return c.send(ctx, method, path, contentType, body, out)
}

// send performs the request with retry/backoff on 429/5xx and error translation.
// A fresh body reader is created each attempt so retries are safe. It is the
// shared transport for both JSON (Do) and multipart (DoMultipart) requests.
func (c *Client) send(ctx context.Context, method, path, contentType string, body []byte, out any) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < c.maxTry; attempt++ {
		if attempt > 0 {
			c.sleep(backoff(attempt))
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", c.apiKey)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = &APIError{Status: resp.StatusCode}
			continue
		}
		if resp.StatusCode >= 400 {
			defer resp.Body.Close()
			return resp, parseAPIError(resp)
		}
		defer resp.Body.Close()
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
				return resp, err
			}
		}
		return resp, nil
	}
	return nil, lastErr
}

func (c *Client) PollOperation(ctx context.Context, opPath string, out any) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var op operation
		if _, err := c.Do(ctx, http.MethodGet, opPath, nil, &op); err != nil {
			return err
		}
		if op.Error != nil {
			return op.Error
		}
		if op.Done {
			if out != nil && len(op.Response) > 0 {
				return json.Unmarshal(op.Response, out)
			}
			return nil
		}
		c.sleep(time.Second)
	}
}

type operation struct {
	Path     string          `json:"path"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response"`
	Error    *APIError       `json:"error"`
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt-1)) * 200 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

func parseAPIError(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	e := &APIError{Status: resp.StatusCode}
	_ = json.Unmarshal(b, e) // best-effort; code/message may be absent
	return e
}
