package provider

import (
	"context"
	"reflect"
	"testing"
)

func TestWarningsDedupeAndOrder(t *testing.T) {
	ctx, w := WithWarnings(context.Background())
	Warn(ctx, "b")
	Warn(ctx, "a")
	Warn(ctx, "b")
	if got := w.List(); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("got %q", got)
	}
}

func TestWarnWithoutCollectorIsNoop(t *testing.T) {
	Warn(context.Background(), "ignored") // must not panic
	var w *Warnings
	if w.List() != nil {
		t.Fatal("nil collector lists nothing")
	}
}
