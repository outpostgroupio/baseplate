package provider

import "testing"

func TestChangeActionString(t *testing.T) {
	if ActionCreate.String() != "create" {
		t.Fatalf("got %q", ActionCreate.String())
	}
	if ActionDelete.String() != "delete" {
		t.Fatalf("got %q", ActionDelete.String())
	}
}
