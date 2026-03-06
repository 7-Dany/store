package token

import "context"

// contextKey is an unexported type that prevents key collisions with other
// packages that also store values in a context.
type contextKey int

const (
	contextKeyUserID    contextKey = iota
	contextKeySessionID contextKey = iota
	contextKeyJTI       contextKey = iota
)

// UserIDFromContext returns the authenticated user's ID injected by Auth middleware.
// The second return value is false if no user ID is present in ctx.
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeyUserID).(string)
	return v, ok
}

// SessionIDFromContext returns the session ID injected by Auth middleware.
// The second return value is false if no session ID is present in ctx.
func SessionIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeySessionID).(string)
	return v, ok
}

// JTIFromContext returns the JTI (jti claim) of the current access token,
// injected by the Auth middleware.
// The second return value is false if no JTI is present in ctx.
func JTIFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeyJTI).(string)
	return v, ok
}

// InjectUserIDForTest writes userID into ctx using the same key that Auth
// middleware uses. Call this from handler test suites to bypass the full JWT
// middleware while still exercising authenticated code paths.
//
// This function must never be called from production code.
func InjectUserIDForTest(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, contextKeyUserID, userID)
}

// InjectJTIForTest writes jti into ctx using the same key that Auth
// middleware uses.
//
// This function must never be called from production code.
func InjectJTIForTest(ctx context.Context, jti string) context.Context {
	return context.WithValue(ctx, contextKeyJTI, jti)
}

// InjectSessionIDForTest writes sessionID into ctx using the same key that
// Auth middleware uses.
//
// This function must never be called from production code.
func InjectSessionIDForTest(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, contextKeySessionID, sessionID)
}
