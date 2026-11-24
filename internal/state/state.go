package state

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/outpostgroupio/baseplate/internal/addr"
)

type Resource struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Key is the for_each instance key; empty for a resource without for_each.
	Key          string                     `json:"index_key,omitempty"`
	ID           string                     `json:"id"`
	Attributes   map[string]json.RawMessage `json:"attributes"`
	Triggers     map[string]json.RawMessage `json:"triggers,omitempty"`
	Dependencies []string                   `json:"dependencies,omitempty"`
}

func (r Resource) Address() string { return addr.Format(r.Type, r.Name, r.Key) }

type State struct {
	Version   int        `json:"version"`
	Serial    int        `json:"serial"`
	Lineage   string     `json:"lineage"`
	Resources []Resource `json:"resources"`
	// Retired lists objects a replace left in place (provider.Replacer's
	// ReplaceAbandons), no longer managed, for `baseplate cleanup` to remove
	// once nothing uses them.
	Retired []Retired `json:"retired,omitempty"`
}

// Retired is an object a replace left in place.
type Retired struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	// Address is where it was managed, and ReplacedBy the id of the object
	// that replaced it there.
	Address    string    `json:"address"`
	ReplacedBy string    `json:"replaced_by"`
	RetiredAt  time.Time `json:"retired_at"`
}

// CurrentVersion is the state-file format version this build writes and reads.
// A file with a higher version is rejected (forward-incompatible); lower
// versions are migrated up via the migrations table.
//
// Version 2 added Resource.Key (`index_key`) for for_each instances. The bump
// exists so an older binary refuses a state file that may hold keyed resources
// (it would otherwise collapse every instance onto one `type.name` address).
//
// Version 3 added Retired. An older binary would drop the list when it
// rewrote the state, losing track of objects waiting for cleanup.
const CurrentVersion = 3

func New() *State {
	return &State{Version: CurrentVersion, Lineage: newLineage()}
}

// migrator upgrades a state document (generic JSON form) from version v to v+1,
// so it may add/rename/reshape fields. applyMigrations stamps the version.
type migrator func(map[string]json.RawMessage) (map[string]json.RawMessage, error)

// migrations[v] upgrades v -> v+1.
var migrations = map[int]migrator{
	// v1 -> v2: index_key is new and optional; a v1 resource is singular, which
	// is exactly an absent index_key. Nothing to rewrite.
	1: func(doc map[string]json.RawMessage) (map[string]json.RawMessage, error) { return doc, nil },
	// v2 -> v3: retired is new and optional.
	2: func(doc map[string]json.RawMessage) (map[string]json.RawMessage, error) { return doc, nil },
}

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
	return unmarshal(b, CurrentVersion, migrations)
}

// unmarshal is Unmarshal with the supported version and migration table
// injected, so the forward-incompatibility gate can be tested as if this were
// an older binary.
func unmarshal(b []byte, current int, table map[int]migrator) (*State, error) {
	var peek struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &peek); err != nil {
		return nil, err
	}
	switch {
	case peek.Version <= 0:
		return nil, fmt.Errorf("state file is missing a version (corrupt or not a baseplate state file)")
	case peek.Version > current:
		return nil, fmt.Errorf("state file was written by a newer version of baseplate (state version %d, this build supports %d); upgrade baseplate", peek.Version, current)
	case peek.Version < current:
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, err
		}
		doc, err := applyMigrations(doc, peek.Version, current, table)
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
		r := &s.Resources[i]
		if addr.Matches(r.Type, r.Name, r.Key, address) {
			return &s.Resources[i], true
		}
	}
	return nil, false
}

// find looks a resource up by its parts, without rendering any address.
func (s *State) find(typ, name, key string) (*Resource, bool) {
	for i := range s.Resources {
		r := &s.Resources[i]
		if r.Type == typ && r.Name == name && r.Key == key {
			return r, true
		}
	}
	return nil, false
}

// FindByID returns the resource of type typ whose remote id is id, if any.
func (s *State) FindByID(typ, id string) (*Resource, bool) {
	for i := range s.Resources {
		if s.Resources[i].Type == typ && s.Resources[i].ID == id {
			return &s.Resources[i], true
		}
	}
	return nil, false
}

func (s *State) Upsert(r Resource) {
	if existing, ok := s.find(r.Type, r.Name, r.Key); ok {
		*existing = r
		return
	}
	s.Resources = append(s.Resources, r)
}

func (s *State) Remove(address string) {
	out := s.Resources[:0]
	for _, r := range s.Resources {
		if !addr.Matches(r.Type, r.Name, r.Key, address) {
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
