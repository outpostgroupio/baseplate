package graph

import (
	"reflect"
	"testing"
)

func TestTopoSortDeterministic(t *testing.T) {
	nodes := []Node{
		{Address: "mock_child.c", DependsOn: []string{"mock_thing.a"}},
		{Address: "mock_thing.a"},
		{Address: "mock_thing.b"},
	}
	order, err := TopoSort(nodes)
	if err != nil {
		t.Fatal(err)
	}
	// a and b have no deps → sorted lexically; c after a.
	want := []string{"mock_thing.a", "mock_thing.b", "mock_child.c"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("got %v want %v", order, want)
	}
}

func TestTopoSortCycle(t *testing.T) {
	nodes := []Node{
		{Address: "a", DependsOn: []string{"b"}},
		{Address: "b", DependsOn: []string{"a"}},
	}
	if _, err := TopoSort(nodes); err == nil {
		t.Fatal("expected cycle error")
	}
}
