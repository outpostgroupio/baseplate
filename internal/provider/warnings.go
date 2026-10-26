package provider

import (
	"context"
	"sync"
)

// Warnings collects non-fatal diagnostics that resources report while the
// engine runs them (e.g. a best-effort lookup for a computed output that
// failed). It is carried in the context (WithWarnings), so any Resource method
// can report one without a signature change; the CLI prints them next to the
// plan's own warnings. Identical messages are kept once, so a resource can
// report a cause shared by many instances (a missing API-key scope) with one
// message instead of one per instance.
type Warnings struct {
	mu   sync.Mutex
	seen map[string]bool
	list []string
}

type warningsKey struct{}

// WithWarnings returns a context carrying a new collector, and the collector.
func WithWarnings(ctx context.Context) (context.Context, *Warnings) {
	w := &Warnings{seen: map[string]bool{}}
	return context.WithValue(ctx, warningsKey{}, w), w
}

// Warn records msg on the context's collector. Without one (e.g. in tests, or
// a caller that doesn't render warnings) it does nothing.
func Warn(ctx context.Context, msg string) {
	w, _ := ctx.Value(warningsKey{}).(*Warnings)
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[msg] {
		return
	}
	w.seen[msg] = true
	w.list = append(w.list, msg)
}

// List returns the warnings recorded so far, in the order first reported.
func (w *Warnings) List() []string {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.list...)
}
