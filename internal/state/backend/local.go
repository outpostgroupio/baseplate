package backend

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"

	"github.com/outpostgroup/baseplate/internal/state"
)

type Local struct {
	path string
	lock *flock.Flock
}

func NewLocal(path string) *Local {
	return &Local{path: path, lock: flock.New(path + ".lock")}
}

func (l *Local) Get() (*state.State, error) {
	b, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return state.New(), nil
	}
	if err != nil {
		return nil, err
	}
	return state.Unmarshal(b)
}

func (l *Local) Put(s *state.State) error {
	b, err := s.Marshal()
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

func (l *Local) Lock() (func() error, error) {
	ok, err := l.lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("state is locked by another process")
	}
	return l.lock.Unlock, nil
}
