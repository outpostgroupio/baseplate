package mock

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestCreateThenRead(t *testing.T) {
	p := New()
	r := p.Resources()["mock_thing"]
	id, _, err := r.Create(context.Background(), map[string]cty.Value{"name": cty.StringVal("a")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Read(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got["name"].AsString() != "a" {
		t.Fatalf("got %v", got["name"])
	}
}
