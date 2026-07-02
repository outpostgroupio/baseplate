package backend

import "github.com/outpostgroup/baseplate/internal/state"

type Backend interface {
	Get() (*state.State, error)
	Put(*state.State) error
	Lock() (unlock func() error, err error)
}
