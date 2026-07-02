package state

import "testing"

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
