package provider

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type deniedErr struct{ denied bool }

func (e deniedErr) Error() string      { return "API 403: Scope not authorized." }
func (e deniedErr) AccessDenied() bool { return e.denied }

type scopedResource struct{ Resource }

func (scopedResource) Doc() ResourceDoc {
	return ResourceDoc{Scopes: []string{"thing:read", "thing:write"}}
}

func TestResourceError(t *testing.T) {
	r := scopedResource{}
	denied := fmt.Errorf("wrapped: %w", deniedErr{denied: true})

	err := ResourceError("x_thing.a", "x_thing", r, denied)
	want := "x_thing.a: wrapped: API 403: Scope not authorized. Check that the API key has the scopes x_thing needs: thing:read, thing:write (see " + ScopesDoc + ")"
	if err.Error() != want {
		t.Fatalf("got  %q\nwant %q", err.Error(), want)
	}
	var d deniedErr
	if !errors.As(err, &d) {
		t.Fatal("the original error must stay reachable")
	}

	// An error that already explains the refusal gets only the address.
	if got := ResourceError("x_thing.a", "x_thing", r, deniedErr{}).Error(); strings.Contains(got, "Check that") {
		t.Fatalf("explained error got a scope hint: %q", got)
	}
	// Other errors get only the address.
	if got := ResourceError("x_thing.a", "x_thing", r, errors.New("boom")).Error(); got != "x_thing.a: boom" {
		t.Fatalf("got %q", got)
	}
	// So does a refusal for a resource with no documented scopes.
	if got := ResourceError("x_thing.a", "x_thing", struct{ Resource }{}, denied).Error(); got != "x_thing.a: wrapped: API 403: Scope not authorized." {
		t.Fatalf("got %q", got)
	}
	if ResourceError("x_thing.a", "x_thing", r, nil) != nil {
		t.Fatal("nil must stay nil")
	}
	if err := ResourceError("x_thing.a", "x_thing", r, ErrNotFound); err != ErrNotFound {
		t.Fatalf("ErrNotFound must pass through unchanged, got %v", err)
	}
}
