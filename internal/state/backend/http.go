package backend

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/outpostgroupio/baseplate/internal/state"
)

// HTTPConfig configures an HTTP state backend speaking the OpenTofu/Terraform
// "http" backend protocol. Only Address is required.
type HTTPConfig struct {
	Address       string
	UpdateMethod  string
	LockAddress   string
	LockMethod    string
	UnlockAddress string
	UnlockMethod  string
	Username      string
	Password      string
	Version       string
	Client        *http.Client
}

// requireTLS rejects a plain-http address unless it points at this machine:
// the backend sends the state, and any basic-auth password, with every request.
func requireTLS(address string) error {
	if address == "" {
		return nil
	}
	u, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("http backend: invalid address %q: %w", address, err)
	}
	if u.Scheme == "http" {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
		return fmt.Errorf("http backend: %s must use https (plain http is only allowed for localhost)", address)
	}
	return nil
}

type HTTP struct {
	cfg      HTTPConfig
	client   *http.Client
	lockID   string
	lockBody []byte
}

func NewHTTP(cfg HTTPConfig) (*HTTP, error) {
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, fmt.Errorf("http backend: address is required")
	}
	for _, a := range []string{cfg.Address, cfg.LockAddress, cfg.UnlockAddress} {
		if err := requireTLS(a); err != nil {
			return nil, err
		}
	}
	if cfg.UpdateMethod == "" {
		cfg.UpdateMethod = http.MethodPost
	}
	if cfg.LockMethod == "" {
		cfg.LockMethod = "LOCK"
	}
	if cfg.UnlockMethod == "" {
		cfg.UnlockMethod = "UNLOCK"
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTP{cfg: cfg, client: client}, nil
}

func (h *HTTP) request(method, url string, body []byte, extra map[string]string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return nil, err
	}
	if h.cfg.Username != "" || h.cfg.Password != "" {
		req.SetBasicAuth(h.cfg.Username, h.cfg.Password)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	return h.client.Do(req)
}

func (h *HTTP) Get() (*state.State, error) {
	resp, err := h.request(http.MethodGet, h.cfg.Address, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(b)) == 0 {
			return state.New(), nil
		}
		return state.Unmarshal(b)
	case http.StatusNoContent, http.StatusNotFound:
		return state.New(), nil
	default:
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http backend: get state: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
}

func (h *HTTP) Put(s *state.State) error {
	b, err := s.Marshal()
	if err != nil {
		return err
	}
	target := h.cfg.Address
	if h.lockID != "" {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + "ID=" + url.QueryEscape(h.lockID)
	}
	// Content-MD5 is an integrity advisory the Terraform http backend sends on
	// state writes; some hosts (e.g. GitLab) record it. MD5 here is a checksum,
	// not a security primitive.
	sum := md5.Sum(b)
	resp, err := h.request(h.cfg.UpdateMethod, target, b, map[string]string{
		"Content-MD5": base64.StdEncoding.EncodeToString(sum[:]),
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http backend: put state: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// lockInfo mirrors Terraform's statemgr.LockInfo. NO json tags: the wire keys
// must be the capitalized Go field names, which TF-http hosts expect.
type lockInfo struct {
	ID        string
	Operation string
	Info      string
	Who       string
	Version   string
	Created   time.Time
	Path      string
}

func (h *HTTP) Lock() (func() error, error) {
	if h.cfg.LockAddress == "" {
		return func() error { return nil }, nil // locking disabled (TF semantics)
	}
	info := lockInfo{
		ID: newLockID(), Operation: "baseplate", Who: who(),
		Version: h.cfg.Version, Created: time.Now().UTC(), Path: h.cfg.Address,
	}
	body, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	resp, err := h.request(h.cfg.LockMethod, h.cfg.LockAddress, body, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		h.lockID = info.ID
		h.lockBody = body
		return h.unlock, nil
	case resp.StatusCode == http.StatusLocked || resp.StatusCode == http.StatusConflict:
		var held lockInfo
		b, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(b, &held)
		holder := held.Who
		if holder == "" {
			holder = "another process"
		}
		return nil, fmt.Errorf("http backend: state locked by %s (ID %s)", holder, held.ID)
	default:
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http backend: lock: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
}

func (h *HTTP) unlock() error {
	addr := h.cfg.UnlockAddress
	if addr == "" {
		addr = h.cfg.LockAddress
	}
	if addr == "" {
		return nil
	}
	resp, err := h.request(h.cfg.UnlockMethod, addr, h.lockBody, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http backend: unlock: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	h.lockID = ""
	h.lockBody = nil
	return nil
}

func newLockID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// who returns a best-effort "user@host" for the lock info; it never fails.
// Precedence: os/user (pure-Go fallback under CGO_ENABLED=0), else $USER, else "unknown".
func who() string {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	if name == "" {
		name = "unknown"
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return name + "@" + host
	}
	return name
}
