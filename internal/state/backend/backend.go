package backend

import "github.com/outpostgroupio/baseplate/internal/state"

type Backend interface {
	Get() (*state.State, error)
	Put(*state.State) error
	Lock() (unlock func() error, err error)
}
