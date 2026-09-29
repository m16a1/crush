package agent

import "context"

// retryContextKey is the unexported context key that carries a retry
// request from the workspace boundary (AppWorkspace / backend.SendMessage)
// down into coordinator.Run without changing the Coordinator.Run signature.
// A retry re-issues the session's most recent prompt instead of sending a
// new one.
type retryContextKey struct{}

// WithRetry returns ctx tagged as an error-recovery retry: the run
// re-issues the session's most recent prompt against the existing
// history instead of appending a new user message. The previous
// attempt's trailing output is discarded so the retried answer replaces
// it. Safe to call on any context.
func WithRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, retryContextKey{}, true)
}

// RetryRequested reports whether [WithRetry] tagged ctx. Exported so the
// coordinator and tests can read it; safe to call on any context.
func RetryRequested(ctx context.Context) bool {
	v, _ := ctx.Value(retryContextKey{}).(bool)
	return v
}
