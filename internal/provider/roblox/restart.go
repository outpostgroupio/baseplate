package roblox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

var _ provider.ServerRestarter = (*Provider)(nil)

// ValidateRestart implements provider.ServerRestarter. The stable
// restartServers endpoint takes the bleed-off as whole minutes in 1–60.
func (p *Provider) ValidateRestart(req provider.RestartRequest) error {
	_, err := toRestartServersRequest(req)
	return err
}

// RestartServers implements provider.ServerRestarter via the stable
// POST /cloud/v2/universes/{id}:restartServers endpoint (scope universe:write).
func (p *Provider) RestartServers(ctx context.Context, req provider.RestartRequest) error {
	r, err := toRestartServersRequest(req)
	if err != nil {
		return err
	}
	if r.UniverseID == "" {
		return errors.New("restart servers: universe id is required")
	}
	if p.restarter == nil {
		return errors.New("restart servers: provider has no Open Cloud client")
	}
	return p.restarter.RestartServers(ctx, r)
}

func toRestartServersRequest(req provider.RestartRequest) (opencloud.RestartServersRequest, error) {
	out := opencloud.RestartServersRequest{UniverseID: req.UniverseID, CloseAllVersions: req.AllVersions}
	switch {
	case req.BleedOff < 0:
		return out, fmt.Errorf("bleed-off must not be negative, got %s", req.BleedOff)
	case req.BleedOff%time.Minute != 0:
		return out, fmt.Errorf("bleed-off must be a whole number of minutes (e.g. 5m), got %s", req.BleedOff)
	case req.BleedOff > opencloud.MaxBleedOffMinutes*time.Minute:
		return out, fmt.Errorf("bleed-off must be at most %dm, got %s", opencloud.MaxBleedOffMinutes, req.BleedOff)
	}
	out.BleedOffMinutes = int(req.BleedOff / time.Minute)
	for _, s := range req.PlaceIDs {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			return out, fmt.Errorf("place id %q is not a valid numeric place id", s)
		}
		out.PlaceIDs = append(out.PlaceIDs, id)
	}
	return out, nil
}
