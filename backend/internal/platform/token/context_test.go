package token_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/platform/token"
)

func TestUserIDFromContext_Present(t *testing.T) {
	t.Parallel()
	ctx := token.InjectUserIDForTest(context.Background(), "user-123")
	got, ok := token.UserIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "user-123", got)
}

func TestUserIDFromContext_Absent(t *testing.T) {
	t.Parallel()
	_, ok := token.UserIDFromContext(context.Background())
	require.False(t, ok)
}

func TestUserIDFromContext_EmptyString(t *testing.T) {
	t.Parallel()
	ctx := token.InjectUserIDForTest(context.Background(), "")
	got, ok := token.UserIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "", got)
}

func TestSessionIDFromContext_Present(t *testing.T) {
	t.Parallel()
	ctx := token.InjectSessionIDForTest(context.Background(), "session-456")
	got, ok := token.SessionIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "session-456", got)
}

func TestSessionIDFromContext_Absent(t *testing.T) {
	t.Parallel()
	_, ok := token.SessionIDFromContext(context.Background())
	require.False(t, ok)
}

// ── JTI helpers ──────────────────────────────────────────────────────────────

func TestJTIFromContext_Present(t *testing.T) {
	t.Parallel()
	ctx := token.InjectJTIForTest(context.Background(), "test-jti-value")
	got, ok := token.JTIFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "test-jti-value", got)
}

func TestJTIFromContext_Absent(t *testing.T) {
	t.Parallel()
	_, ok := token.JTIFromContext(context.Background())
	require.False(t, ok)
}

func TestJTIFromContext_EmptyString(t *testing.T) {
	t.Parallel()
	ctx := token.InjectJTIForTest(context.Background(), "")
	got, ok := token.JTIFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "", got)
}

func TestInjectJTIForTest_AllThreeKeysIndependent(t *testing.T) {
	t.Parallel()
	ctx := token.InjectUserIDForTest(context.Background(), "u1")
	ctx = token.InjectSessionIDForTest(ctx, "s1")
	ctx = token.InjectJTIForTest(ctx, "j1")

	uid, uok := token.UserIDFromContext(ctx)
	require.True(t, uok)
	require.Equal(t, "u1", uid)

	sid, sok := token.SessionIDFromContext(ctx)
	require.True(t, sok)
	require.Equal(t, "s1", sid)

	jti, jok := token.JTIFromContext(ctx)
	require.True(t, jok)
	require.Equal(t, "j1", jti)
}

func TestInjectBothContextValues(t *testing.T) {
	t.Parallel()
	ctx := token.InjectUserIDForTest(context.Background(), "u1")
	ctx = token.InjectSessionIDForTest(ctx, "s1")

	uid, uok := token.UserIDFromContext(ctx)
	sid, sok := token.SessionIDFromContext(ctx)
	require.True(t, uok)
	require.True(t, sok)
	require.Equal(t, "u1", uid)
	require.Equal(t, "s1", sid)
}

func TestContextKeysDoNotCollide(t *testing.T) {
	t.Parallel()
	ctx := token.InjectUserIDForTest(context.Background(), "user-only")
	_, sidOK := token.SessionIDFromContext(ctx)
	require.False(t, sidOK, "sessionID key must not be satisfied by userID injection")
	_, jtiOK := token.JTIFromContext(ctx)
	require.False(t, jtiOK, "JTI key must not be satisfied by userID injection")

	ctx2 := token.InjectSessionIDForTest(context.Background(), "session-only")
	_, uidOK := token.UserIDFromContext(ctx2)
	require.False(t, uidOK, "userID key must not be satisfied by sessionID injection")
	_, jtiOK2 := token.JTIFromContext(ctx2)
	require.False(t, jtiOK2, "JTI key must not be satisfied by sessionID injection")

	ctx3 := token.InjectJTIForTest(context.Background(), "jti-only")
	_, uidOK3 := token.UserIDFromContext(ctx3)
	require.False(t, uidOK3, "userID key must not be satisfied by JTI injection")
	_, sidOK3 := token.SessionIDFromContext(ctx3)
	require.False(t, sidOK3, "sessionID key must not be satisfied by JTI injection")
}
