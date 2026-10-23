package opencloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// MaxBleedOffMinutes is the longest bleed-off the stable restartServers
// endpoint accepts (it rejects 0 or >60 with INVALID_ARGUMENT when bleed-off is
// on; verified live).
const MaxBleedOffMinutes = 60

// RestartServersRequest is one call to the stable
// POST /cloud/v2/universes/{id}:restartServers endpoint.
type RestartServersRequest struct {
	UniverseID string
	// PlaceIDs restricts the restart to these places; empty = all places.
	PlaceIDs []int64
	// CloseAllVersions restarts servers on every version, not just outdated ones.
	CloseAllVersions bool
	// BleedOffMinutes > 0 enables bleed-off (stop matchmaking, shut down after
	// this many minutes, 1–60). 0 shuts servers down immediately.
	BleedOffMinutes int
}

// ServerRestartClient restarts a universe's live game servers.
//
// It targets the STABLE Cloud v2 endpoint (scope universe:write). The BETA
// server-management API (/server-management/v1/universes/{id}/restarts) adds a
// 240-minute bleed-off, an attributes payload and restart status/forecast
// reads; it can be added as a second implementation behind the same
// provider.ServerRestarter seam once it leaves beta.
type ServerRestartClient interface {
	RestartServers(ctx context.Context, req RestartServersRequest) error
}

type serverRestartClient struct{ c *Client }

// NewServerRestartClient builds a ServerRestartClient over the root Open Cloud
// client (base https://apis.roblox.com).
func NewServerRestartClient(c *Client) ServerRestartClient { return &serverRestartClient{c: c} }

type restartServersBody struct {
	PlaceIDs                []int64 `json:"placeIds,omitempty"`
	CloseAllVersions        bool    `json:"closeAllVersions"`
	BleedOffServers         bool    `json:"bleedOffServers"`
	BleedOffDurationMinutes int     `json:"bleedOffDurationMinutes,omitempty"`
}

func (rc *serverRestartClient) RestartServers(ctx context.Context, req RestartServersRequest) error {
	if req.UniverseID == "" {
		return errors.New("restart servers: universe id is required")
	}
	if req.BleedOffMinutes < 0 || req.BleedOffMinutes > MaxBleedOffMinutes {
		return fmt.Errorf("restart servers: bleed-off must be between 1 and %d minutes (or 0 for an immediate restart), got %d",
			MaxBleedOffMinutes, req.BleedOffMinutes)
	}
	body := restartServersBody{
		PlaceIDs:                req.PlaceIDs,
		CloseAllVersions:        req.CloseAllVersions,
		BleedOffServers:         req.BleedOffMinutes > 0,
		BleedOffDurationMinutes: req.BleedOffMinutes,
	}
	// The response is an empty object today ("may later contain a status").
	path := fmt.Sprintf("/cloud/v2/universes/%s:restartServers", url.PathEscape(req.UniverseID))
	_, err := rc.c.Do(ctx, http.MethodPost, path, body, nil)
	return err
}
