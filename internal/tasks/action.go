package tasks

import "context"

type actionContextKey struct{}

// WithAction makes the current mutation visible to shared service code without
// coupling HTTP handlers to a particular transport or execution strategy.
func WithAction(ctx context.Context, a Action) context.Context {
	return context.WithValue(ctx, actionContextKey{}, a)
}

// ActionFrom returns the mutation currently being handled, if any.
func ActionFrom(ctx context.Context) (Action, bool) {
	a, ok := ctx.Value(actionContextKey{}).(Action)
	return a, ok
}
