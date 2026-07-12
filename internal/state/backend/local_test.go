package backend

import (
	"path/filepath"
	"testing"

	"github.com/outpostgroup/baseplate/internal/state"
)

func TestLocalPutGetAndLock(t *testing.T) {
	dir := t.TempDir()
	b := NewLocal(filepath.Join(dir, "baseplate.bpstate"))

	s, err := b.Get() // missing file → fresh
	if err != nil {
		t.Fatal(err)
	}
	s.Upsert(state.Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})
	if err := b.Put(s); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Find("mock_thing.a"); !ok {
		t.Fatal("state not persisted")
	}

	unlock, err := b.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}
