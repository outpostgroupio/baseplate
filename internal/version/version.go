// Package version holds build metadata stamped in at release time via
// -ldflags "-X github.com/outpostgroupio/baseplate/internal/version.<Var>=...".
// The defaults keep an unstamped `go build`/`go install` honest.
package version

import "fmt"

var (
	// Version is the release version (git tag), e.g. "0.1.0-rc.1".
	Version = "dev"
	// Commit is the short commit SHA the binary was built from.
	Commit = "none"
	// Date is the RFC3339 build timestamp.
	Date = "unknown"
)

// String renders a one-line human version banner, e.g.
// "baseplate 0.1.0-rc.1 (commit abc1234, built 2026-07-13T20:00:00Z)".
func String() string {
	return fmt.Sprintf("baseplate %s (commit %s, built %s)", Version, Commit, Date)
}
