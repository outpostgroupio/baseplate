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

func New() *State {
	return &State{Version: 1, Lineage: newLineage()}
}

func (s *State) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func Unmarshal(b []byte) (*State, error) {
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Version != 1 {
		return nil, fmt.Errorf("unsupported state version %d (want 1)", s.Version)
	}
	return &s, nil
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
