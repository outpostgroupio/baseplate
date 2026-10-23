package provider

import (
	"context"
	"time"
)

// ServerRestarter is an optional Provider capability: restart an experience's
// live game servers so players move onto the latest published place version.
// It is an imperative action, not reconcilable state, so it is exposed as a CLI
// verb (`baseplate restart-servers`, `apply --restart-servers`) rather than a
// resource. The CLI type-asserts the active provider for it; a provider that
// doesn't implement it makes those commands fail with a clear error.
type ServerRestarter interface {
	// ValidateRestart checks req's options (not UniverseID, which a caller may
	// only learn later) without side effects, so a caller can reject bad flags
	// before prompting or applying anything.
	ValidateRestart(req RestartRequest) error
	// RestartServers requests the restart. It returns once Roblox has accepted
	// the request; servers then drain/shut down asynchronously.
	RestartServers(ctx context.Context, req RestartRequest) error
}

// RestartRequest describes one restart of a universe's game servers.
type RestartRequest struct {
	// UniverseID is the universe whose servers are restarted. Required.
	UniverseID string
	// PlaceIDs restricts the restart to these places. Empty = every place in
	// the universe.
	PlaceIDs []string
	// AllVersions restarts servers on every place version, including the
	// current one. When false, only servers running an older version than the
	// latest published one are restarted.
	AllVersions bool
	// BleedOff, when > 0, stops matchmaking into the affected servers and lets
	// them run for this long before shutting down, so players can drain onto
	// new servers naturally. Zero shuts the servers down immediately. It must
	// be a whole number of minutes within the provider's supported range.
	BleedOff time.Duration
}
