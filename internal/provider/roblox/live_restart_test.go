package roblox

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/outpostgroupio/baseplate/internal/provider"
)

// TestLiveRestartServers performs ONE real restart of the test universe's
// servers (outdated versions only, 1-minute bleed-off) via the stable
// restartServers endpoint. It needs the universe:write scope. The test
// universe is a throwaway with no real players, so this is safe there; never
// point BASEPLATE_TEST_UNIVERSE at a live experience.
//
//	set -a; . ./.env; set +a
//	BASEPLATE_LIVE=1 go test ./internal/provider/roblox/ -run LiveRestart -v
func TestLiveRestartServers(t *testing.T) {
	if os.Getenv("BASEPLATE_LIVE") != "1" {
		t.Skip("set BASEPLATE_LIVE=1 (+ ROBLOX_API_KEY, BASEPLATE_TEST_UNIVERSE) to run the live test")
	}
	key := os.Getenv("ROBLOX_API_KEY")
	universe := os.Getenv("BASEPLATE_TEST_UNIVERSE")
	if key == "" || universe == "" {
		t.Fatal("ROBLOX_API_KEY and BASEPLATE_TEST_UNIVERSE must be set")
	}
	p, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	var rs provider.ServerRestarter = p
	if err := rs.RestartServers(context.Background(), provider.RestartRequest{
		UniverseID: universe,
		BleedOff:   time.Minute,
	}); err != nil {
		t.Fatalf("restart servers (needs the universe:write scope): %v", err)
	}
}
