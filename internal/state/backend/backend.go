package backend

import "github.com/outpostgroupio/baseplate/internal/state"

type Backend interface {
	Get() (*state.State, error)
	Put(*state.State) error
	// Lock takes the state lock for operation (the command, e.g. "apply"). A
	// backend that records lock details stores it, so a run that is blocked can
	// say who holds the lock and why.
	Lock(operation string) (unlock func() error, err error)
}

// ForceUnlocker is implemented by backends whose lock can outlive the process
// that took it, e.g. a remote lock left behind by a cancelled CI job.
// ForceUnlock releases the lock with the given id (baseplate force-unlock).
type ForceUnlocker interface {
	ForceUnlock(id string) error
}
