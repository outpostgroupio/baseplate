package provider

import (
	"errors"
	"fmt"
	"strings"
)

// ScopesDoc is where the API-key scopes each resource needs are listed.
const ScopesDoc = "https://github.com/outpostgroupio/baseplate/blob/main/docs/how-to/api-key-scopes.md"

// AccessDenied is implemented by provider errors that may report a refused
// credential (an HTTP 401 or 403). An error that already explains a refusal
// (for example, an experience that isn't eligible for a feature) implements
// it and returns false, so ResourceError doesn't add a misleading scope hint.
type AccessDenied interface {
	AccessDenied() bool
}

// ResourceError attributes err, from a call to the resource of type typ at
// address, to that resource. When the provider refused the credential, it also names the
// API-key scopes the resource's docs list, since a missing scope is the usual
// cause. ErrNotFound and nil pass through unchanged.
func ResourceError(address, typ string, r Resource, err error) error {
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	var ad AccessDenied
	if !errors.As(err, &ad) || !ad.AccessDenied() {
		return fmt.Errorf("%s: %w", address, err)
	}
	d, ok := r.(Documented)
	if !ok || len(d.Doc().Scopes) == 0 {
		return fmt.Errorf("%s: %w", address, err)
	}
	return &accessError{address: address, typ: typ, scopes: d.Doc().Scopes, err: err}
}

type accessError struct {
	address string
	typ     string
	scopes  []string
	err     error
}

func (e *accessError) Error() string {
	return fmt.Sprintf("%s: %v. Check that the API key has the scopes %s needs: %s (see %s)",
		e.address, strings.TrimSuffix(e.err.Error(), "."), e.typ, strings.Join(e.scopes, ", "), ScopesDoc)
}

func (e *accessError) Unwrap() error { return e.err }
