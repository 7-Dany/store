package rbac

import (
	"context"
	"encoding/json"
)

// contextKey is an unexported type that prevents key collisions with other
// packages that also store values in a context.
type contextKey int

const (
	accessResultKey    contextKey = iota
	testPermissionsKey contextKey = iota
)

// AccessResult is the full access context injected by Require into every request
// that passes the permission check, including those with access_type = "request".
// Downstream middleware (ApprovalGate) and handlers read from this — never from the DB.
type AccessResult struct {
	Permission    string          // canonical permission that was checked, e.g. "job_queue:configure"
	IsOwner       bool
	HasPermission bool
	AccessType    string          // "direct" | "conditional" | "request" | "denied"
	Scope         string          // "own" | "all"
	Conditions    json.RawMessage // '{}' when no conditions apply
}

// InjectPermissionsForTest writes a set of allowed permission strings into ctx.
// Require checks this set before hitting the DB.
// Call only from test code — never from production paths.
func InjectPermissionsForTest(ctx context.Context, perms ...string) context.Context {
	set := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return context.WithValue(ctx, testPermissionsKey, set)
}

// HasPermissionInContext checks test-injected permissions.
// Returns (false, false) when no test set is present — Require falls through to DB.
// Returns (true,  true)  when the permission is in the test set.
// Returns (false, true)  when a test set exists but this permission is not in it.
func HasPermissionInContext(ctx context.Context, permission string) (allowed, found bool) {
	set, ok := ctx.Value(testPermissionsKey).(map[string]struct{})
	if !ok || set == nil {
		return false, false
	}
	_, has := set[permission]
	return has, true
}

// AccessResultFromContext returns the AccessResult injected by Require.
// Returns nil when called outside a Require-guarded route.
func AccessResultFromContext(ctx context.Context) *AccessResult {
	v, _ := ctx.Value(accessResultKey).(*AccessResult)
	return v
}

// ScopeFromContext returns the scope ("own"|"all") from the current AccessResult.
// Returns "own" — the safe default — when not set.
func ScopeFromContext(ctx context.Context) string {
	if r := AccessResultFromContext(ctx); r != nil {
		return r.Scope
	}
	return "own"
}

// ConditionsFromContext returns the conditions JSONB from the current AccessResult.
// Returns json.RawMessage("{}") when not set.
func ConditionsFromContext(ctx context.Context) json.RawMessage {
	if r := AccessResultFromContext(ctx); r != nil {
		return r.Conditions
	}
	return json.RawMessage("{}")
}

// injectAccessResult stores r into ctx under the access result key.
// Called only from checker.go — not exported.
func injectAccessResult(ctx context.Context, r *AccessResult) context.Context {
	return context.WithValue(ctx, accessResultKey, r)
}
