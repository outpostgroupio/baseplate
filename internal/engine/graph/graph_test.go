package graph

import (
	"reflect"
	"strings"
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

func TestTopoSortUnknownDependency(t *testing.T) {
	nodes := []Node{
		{Address: "mock_child.c", DependsOn: []string{"mock_thing.missing"}},
	}
	_, err := TopoSort(nodes)
	if err == nil {
		t.Fatal("expected unknown-dependency error")
	}
	if !strings.Contains(err.Error(), "unknown resource mock_thing.missing") {
		t.Fatalf("error should name the unknown dependency, got: %v", err)
	}
}

func TestReverse(t *testing.T) {
	if got := Reverse([]string{"a", "b", "c"}); !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Fatalf("got %v", got)
	}
	if got := Reverse(nil); len(got) != 0 {
		t.Fatalf("reverse of nil should be empty, got %v", got)
	}
}
