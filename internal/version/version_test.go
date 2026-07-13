package version

import "testing"

func TestStringDefaults(t *testing.T) {
	// Defaults keep a plain `go build`/`go install` honest.
	if got, want := String(), "baseplate dev (commit none, built unknown)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestStringStamped(t *testing.T) {
	// Simulate an ldflags-stamped build by overriding the vars.
	old := [3]string{Version, Commit, Date}
	defer func() { Version, Commit, Date = old[0], old[1], old[2] }()
	Version, Commit, Date = "0.1.0-rc.1", "abc1234", "2026-07-13T20:00:00Z"
	if got, want := String(), "baseplate 0.1.0-rc.1 (commit abc1234, built 2026-07-13T20:00:00Z)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
