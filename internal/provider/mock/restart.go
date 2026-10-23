package mock

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// Restarter is an in-memory provider.ServerRestarter that records every
// accepted request. It is deliberately NOT part of the default mock Provider:
// a credential-less `baseplate restart-servers` should fail loudly rather than
// pretend to restart live servers. Tests compose it with a Provider, e.g.
//
//	struct{ *mock.Provider; *mock.Restarter }{mock.New(), &mock.Restarter{}}
type Restarter struct {
	mu       sync.Mutex
	requests []provider.RestartRequest
	// Err, when set, is returned by RestartServers (after validation) and the
	// request is not recorded.
	Err error
}

var _ provider.ServerRestarter = (*Restarter)(nil)

// ValidateRestart mirrors the real provider's option checks: the bleed-off
// must be a non-negative whole number of minutes.
func (r *Restarter) ValidateRestart(req provider.RestartRequest) error {
	if req.BleedOff < 0 || req.BleedOff%time.Minute != 0 {
		return errors.New("mock: bleed-off must be a non-negative whole number of minutes")
	}
	return nil
}

func (r *Restarter) RestartServers(_ context.Context, req provider.RestartRequest) error {
	if err := r.ValidateRestart(req); err != nil {
		return err
	}
	if req.UniverseID == "" {
		return errors.New("mock: universe id is required")
	}
	if r.Err != nil {
		return r.Err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	req.PlaceIDs = append([]string(nil), req.PlaceIDs...)
	r.requests = append(r.requests, req)
	return nil
}

// Requests returns a copy of the recorded restart requests.
func (r *Restarter) Requests() []provider.RestartRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]provider.RestartRequest(nil), r.requests...)
}
