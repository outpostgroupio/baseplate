package state

import (
	"encoding/json"
	"fmt"
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
	if _, err := Unmarshal([]byte(`{"version":3}`)); err == nil {
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

func TestRoundTripKeyedResource(t *testing.T) {
	s := New()
	s.Upsert(Resource{Type: "roblox_asset", Name: "sprites", Key: "ui/gem.png", ID: "1"})
	s.Upsert(Resource{Type: "roblox_asset", Name: "sprites", Key: "ui/coin.png", ID: "2"})
	b, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"index_key": "ui/gem.png"`) {
		t.Fatalf("index_key not written:\n%s", b)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := got.Find(`roblox_asset.sprites["ui/gem.png"]`)
	if !ok || r.ID != "1" || r.Key != "ui/gem.png" {
		t.Fatalf("keyed instance lost in round trip: %+v", got.Resources)
	}
	if _, ok := got.Find("roblox_asset.sprites"); ok {
		t.Fatal("instances must not collapse onto the bare type.name address")
	}
}

func TestSingularResourceOmitsIndexKey(t *testing.T) {
	s := New()
	s.Upsert(Resource{Type: "mock_thing", Name: "a", ID: "1"})
	b, _ := s.Marshal()
	if strings.Contains(string(b), "index_key") {
		t.Fatalf("singular resource must not write index_key:\n%s", b)
	}
}

func TestV1StateMigratesToV2(t *testing.T) {
	v1 := `{"version":1,"serial":4,"lineage":"abc","resources":[{"type":"mock_thing","name":"a","id":"9","attributes":{}}]}`
	got, err := Unmarshal([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.Serial != 4 || got.Lineage != "abc" {
		t.Fatalf("migrated header wrong: %+v", got)
	}
	if r, ok := got.Find("mock_thing.a"); !ok || r.ID != "9" || r.Key != "" {
		t.Fatalf("v1 resource should load as singular: %+v", got.Resources)
	}
}

// A v2 file (which may contain keyed instances) must be refused by a binary
// that only understands v1, rather than silently collapsing instances.
func TestV2StateRefusedByV1Binary(t *testing.T) {
	s := New()
	s.Upsert(Resource{Type: "roblox_asset", Name: "sprites", Key: "a", ID: "1"})
	b, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	_, err = unmarshal(b, 1, map[int]migrator{})
	if err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("v1 binary must refuse v2 state, got %v", err)
	}
}

func TestFindKeyedAndEscapedAddresses(t *testing.T) {
	s := New()
	for _, k := range []string{"a", `q"uote`, "${x}"} {
		s.Upsert(Resource{Type: "t", Name: "n", Key: k, ID: "id-" + k})
	}
	s.Upsert(Resource{Type: "t", Name: "n2", ID: "solo"})
	for _, r := range s.Resources {
		got, ok := s.Find(r.Address())
		if !ok || got.ID != r.ID {
			t.Fatalf("Find(%s) = %+v, %v", r.Address(), got, ok)
		}
	}
	if _, ok := s.Find(`t.n["missing"]`); ok {
		t.Fatal("Find must miss an absent key")
	}
}

// Find runs once per resource in plan and once per commit in apply, so with
// thousands of for_each instances it must not allocate per comparison.
func TestFindDoesNotAllocate(t *testing.T) {
	s := New()
	for i := 0; i < 500; i++ {
		s.Resources = append(s.Resources, Resource{Type: "roblox_asset", Name: "sprites", Key: fmt.Sprintf("ui/%03d.png", i), ID: fmt.Sprint(i)})
	}
	allocs := testing.AllocsPerRun(20, func() { s.Find(`roblox_asset.sprites["ui/499.png"]`) })
	if allocs != 0 {
		t.Fatalf("Find allocated %v times per call, want 0", allocs)
	}
}
