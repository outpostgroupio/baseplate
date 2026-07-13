package state

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

type Resource struct {
	Type         string                     `json:"type"`
	Name         string                     `json:"name"`
	ID           string                     `json:"id"`
	Attributes   map[string]json.RawMessage `json:"attributes"`
	Triggers     map[string]json.RawMessage `json:"triggers,omitempty"`
	Dependencies []string                   `json:"dependencies,omitempty"`
}

func (r Resource) Address() string { return r.Type + "." + r.Name }

type State struct {
	Version   int        `json:"version"`
	Serial    int        `json:"serial"`
	Lineage   string     `json:"lineage"`
	Resources []Resource `json:"resources"`
}

// CurrentVersion is the state-file format version this build writes and reads.
// A file with a higher version is rejected (forward-incompatible); lower
// versions are migrated up via the migrations table.
const CurrentVersion = 1

func New() *State {
	return &State{Version: CurrentVersion, Lineage: newLineage()}
}

// migrator upgrades a state document (generic JSON form) from version v to v+1,
// so it may add/rename/reshape fields. applyMigrations stamps the version.
type migrator func(map[string]json.RawMessage) (map[string]json.RawMessage, error)

// migrations[v] upgrades v -> v+1. Empty today (CurrentVersion == 1); the first
// future format change adds migrations[1] and bumps CurrentVersion to 2.
var migrations = map[int]migrator{}

// applyMigrations upgrades doc from version `from` to `to`, applying
// table[from], table[from+1], … and stamping doc["version"] after each step.
func applyMigrations(doc map[string]json.RawMessage, from, to int, table map[int]migrator) (map[string]json.RawMessage, error) {
	for v := from; v < to; v++ {
		m, ok := table[v]
		if !ok {
			return nil, fmt.Errorf("no migration path from state version %d", v)
		}
		var err error
		if doc, err = m(doc); err != nil {
			return nil, fmt.Errorf("migrating state version %d: %w", v, err)
		}
		nb, err := json.Marshal(v + 1)
		if err != nil {
			return nil, err
		}
		doc["version"] = nb
	}
	return doc, nil
}

func (s *State) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func Unmarshal(b []byte) (*State, error) {
	var peek struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &peek); err != nil {
		return nil, err
	}
	switch {
	case peek.Version <= 0:
		return nil, fmt.Errorf("state file is missing a version (corrupt or not a baseplate state file)")
	case peek.Version > CurrentVersion:
		return nil, fmt.Errorf("state file was written by a newer version of baseplate (state version %d, this build supports %d); upgrade baseplate", peek.Version, CurrentVersion)
	case peek.Version < CurrentVersion:
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, err
		}
		doc, err := applyMigrations(doc, peek.Version, CurrentVersion, migrations)
		if err != nil {
			return nil, err
		}
		mb, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		var s State
		if err := json.Unmarshal(mb, &s); err != nil {
			return nil, err
		}
		return &s, nil
	default:
		var s State
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, err
		}
		return &s, nil
	}
}

func (s *State) Find(address string) (*Resource, bool) {
	for i := range s.Resources {
		if s.Resources[i].Address() == address {
			return &s.Resources[i], true
		}
	}
	return nil, false
}

func (s *State) Upsert(r Resource) {
	if existing, ok := s.Find(r.Address()); ok {
		*existing = r
		return
	}
	s.Resources = append(s.Resources, r)
}

func (s *State) Remove(address string) {
	out := s.Resources[:0]
	for _, r := range s.Resources {
		if r.Address() != address {
			out = append(out, r)
		}
	}
	s.Resources = out
}

func newLineage() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
