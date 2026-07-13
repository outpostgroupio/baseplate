package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoundTripAndVersionGuard(t *testing.T) {
	s := New()
	s.Upsert(Resource{Type: "mock_thing", Name: "a", ID: "mock_thing/1"})
	b, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Find("mock_thing.a"); !ok {
		t.Fatal("resource not found after round trip")
	}
	if _, err := Unmarshal([]byte(`{"version":2}`)); err == nil {
		t.Fatal("expected version guard error")
	}
}

func TestUnmarshalRoundTripsCurrentVersion(t *testing.T) {
	s := New()
	s.Serial = 3
	s.Resources = append(s.Resources, Resource{Type: "mock_thing", Name: "a", ID: "9"})
	b, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != CurrentVersion || got.Serial != 3 || len(got.Resources) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestNewStampsCurrentVersion(t *testing.T) {
	if New().Version != CurrentVersion {
		t.Fatalf("New() must stamp CurrentVersion (%d), got %d", CurrentVersion, New().Version)
	}
}

func TestUnmarshalRejectsNewerVersion(t *testing.T) {
	_, err := Unmarshal([]byte(`{"version":999,"serial":0,"lineage":"x","resources":[]}`))
	if err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("expected a friendly forward-incompat error, got %v", err)
	}
}

func TestUnmarshalRejectsMissingVersion(t *testing.T) {
	_, err := Unmarshal([]byte(`{"serial":0,"lineage":"x","resources":[]}`))
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected a missing-version error, got %v", err)
	}
}

func TestApplyMigrationsChainsAndStampsVersion(t *testing.T) {
	table := map[int]migrator{
		1: func(d map[string]json.RawMessage) (map[string]json.RawMessage, error) {
			d["added_at_1"] = json.RawMessage(`true`)
			return d, nil
		},
		2: func(d map[string]json.RawMessage) (map[string]json.RawMessage, error) {
			d["added_at_2"] = json.RawMessage(`true`)
			return d, nil
		},
	}
	doc := map[string]json.RawMessage{"version": json.RawMessage(`1`)}
	out, err := applyMigrations(doc, 1, 3, table)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["added_at_1"]; !ok {
		t.Fatal("step 1 did not run")
	}
	if _, ok := out["added_at_2"]; !ok {
		t.Fatal("step 2 did not run (chain stopped early)")
	}
	if string(out["version"]) != "3" {
		t.Fatalf("version should be stamped to 3, got %s", out["version"])
	}
}

func TestApplyMigrationsErrorsOnMissingStep(t *testing.T) {
	table := map[int]migrator{
		1: func(d map[string]json.RawMessage) (map[string]json.RawMessage, error) { return d, nil },
		// no migrator for 2
	}
	_, err := applyMigrations(map[string]json.RawMessage{"version": json.RawMessage(`1`)}, 1, 3, table)
	if err == nil || !strings.Contains(err.Error(), "no migration path") {
		t.Fatalf("expected 'no migration path' error, got %v", err)
	}
}
