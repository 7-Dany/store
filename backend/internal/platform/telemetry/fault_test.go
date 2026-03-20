package telemetry

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// mockNetError satisfies net.Error with a configurable timeout flag.
type mockNetError struct{ timeout bool }

func (e *mockNetError) Error() string   { return "mock net error" }
func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return false }

// pgError returns a *pgconn.PgError with the given SQLSTATE code.
func pgError(code string) *pgconn.PgError { return &pgconn.PgError{Code: code} }

// ── T-1: constructor produces correct Fault ───────────────────────────────────

func TestStore_WrapsWithLayerAndOp(t *testing.T) {
	inner := errors.New("sql: no rows")
	err := Store("GetUser.query", inner)

	var f *Fault
	require.True(t, errors.As(err, &f))
	assert.Equal(t, LayerStore, f.Layer)
	assert.Equal(t, "GetUser.query", f.Op)
	assert.ErrorIs(t, err, inner)
}

func TestAllConstructors_SetCorrectLayer(t *testing.T) {
	inner := errors.New("e")

	cases := []struct {
		name  string
		err   error
		layer Layer
	}{
		{"Store", Store("op", inner), LayerStore},
		{"Service", Service("op", inner), LayerService},
		{"Handler", Handler("op", inner), LayerHandler},
		{"OAuth", OAuth("op", inner), LayerOAuth},
		{"Mailer", Mailer("op", inner), LayerMailer},
		{"Token", Token("op", inner), LayerToken},
		{"Crypto", Crypto("op", inner), LayerCrypto},
		{"KVStore", KVStore("op", inner), LayerKVStore},
		{"RBAC", RBAC("op", inner), LayerRBAC},
		{"Worker", Worker("op", inner), LayerWorker},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.layer, LayerOf(tc.err))
		})
	}
}

// ── T-2: constructors return nil when err is nil ──────────────────────────────

func TestAllConstructors_NilWhenErrNil(t *testing.T) {
	assert.Nil(t, Store("op", nil))
	assert.Nil(t, Service("op", nil))
	assert.Nil(t, Handler("op", nil))
	assert.Nil(t, OAuth("op", nil))
	assert.Nil(t, Mailer("op", nil))
	assert.Nil(t, Token("op", nil))
	assert.Nil(t, Crypto("op", nil))
	assert.Nil(t, KVStore("op", nil))
	assert.Nil(t, RBAC("op", nil))
	assert.Nil(t, Worker("op", nil))
}

// ── T-3: errors.Is traverses through *Fault via Unwrap ───────────────────────

func TestFault_ErrorsIsTraverses(t *testing.T) {
	sentinel := errors.New("sentinel")
	wrapped := Store("op", Service("op2", sentinel))

	assert.ErrorIs(t, wrapped, sentinel,
		"errors.Is must traverse the full Fault chain")
}

// ── T-4: errors.As(*pgconn.PgError) traverses through *Fault ─────────────────

func TestFault_ErrorsAsTraversesPgError(t *testing.T) {
	pg := pgError("23505")
	wrapped := Service("op", Store("q", pg))

	var got *pgconn.PgError
	require.True(t, errors.As(wrapped, &got))
	assert.Equal(t, "23505", got.Code)
}

// ── T-5: LayerOf returns outermost layer ─────────────────────────────────────

func TestLayerOf_ReturnsOutermost(t *testing.T) {
	inner := errors.New("e")
	err := Service("op", Store("op2", inner))
	assert.Equal(t, LayerService, LayerOf(err),
		"LayerOf must return the outermost Fault's layer")
}

// ── T-6: LayerOf returns LayerUnknown when no Fault present ──────────────────

func TestLayerOf_UnknownWhenNoFault(t *testing.T) {
	assert.Equal(t, LayerUnknown, LayerOf(errors.New("plain")))
	assert.Equal(t, LayerUnknown, LayerOf(nil))
}

// ── T-7 … T-8: ClassifyCause — pg SQLSTATE codes ─────────────────────────────

func TestClassifyCause_PgConstraint(t *testing.T) {
	for _, code := range []string{"23505", "23503", "23514", "23502"} {
		t.Run(code, func(t *testing.T) {
			assert.Equal(t, CauseDBConstraint, ClassifyCause(pgError(code)))
		})
	}
}

func TestClassifyCause_PgPool(t *testing.T) {
	for _, code := range []string{"53300", "08006", "08001", "08004"} {
		t.Run(code, func(t *testing.T) {
			assert.Equal(t, CauseDBPool, ClassifyCause(pgError(code)))
		})
	}
}

func TestClassifyCause_PgTimeout(t *testing.T) {
	assert.Equal(t, CauseDBTimeout, ClassifyCause(pgError("57014")))
}

func TestClassifyCause_PgOther(t *testing.T) {
	assert.Equal(t, CauseDB, ClassifyCause(pgError("42601")))
}

// ── T-9: ClassifyCause → timeout for context.DeadlineExceeded ────────────────

func TestClassifyCause_DeadlineExceeded(t *testing.T) {
	assert.Equal(t, CauseTimeout, ClassifyCause(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)))
}

// ── T-10: ClassifyCause → network_error for non-timeout net.Error ────────────

func TestClassifyCause_NetworkError(t *testing.T) {
	assert.Equal(t, CauseNetwork, ClassifyCause(&mockNetError{timeout: false}))
}

func TestClassifyCause_NetTimeout(t *testing.T) {
	assert.Equal(t, CauseTimeout, ClassifyCause(&mockNetError{timeout: true}))
}

// ── T-11: ClassifyCause → client_cancelled for context.Canceled ──────────────

func TestClassifyCause_ContextCanceled(t *testing.T) {
	assert.Equal(t, CauseClientGone, ClassifyCause(fmt.Errorf("wrapped: %w", context.Canceled)))
}

// ── T-12: ClassifyCause → unknown for plain errors.New ───────────────────────

func TestClassifyCause_Unknown(t *testing.T) {
	assert.Equal(t, CauseUnknown, ClassifyCause(errors.New("something unexpected")))
}

// ── T-13: ClassifyCause(nil) returns "" ──────────────────────────────────────

func TestClassifyCause_Nil(t *testing.T) {
	assert.Equal(t, Cause(""), ClassifyCause(nil))
}

// ── T-14: ClassifyCause traverses *Fault to underlying pgErr ─────────────────

func TestClassifyCause_TraversesFaultChain(t *testing.T) {
	pg := pgError("23505")
	err := Service("op", Store("q", pg))
	assert.Equal(t, CauseDBConstraint, ClassifyCause(err),
		"ClassifyCause must traverse Fault wrappers to reach pgErr")
}

// ── T-14b: ClassifyCause → panic for LayerPanic Fault ────────────────────────

func TestClassifyCause_PanicFault(t *testing.T) {
	panicErr := &Fault{
		Op:    "http.handler",
		Layer: LayerPanic,
		Err:   fmt.Errorf("panic: index out of range"),
	}
	assert.Equal(t, CausePanic, ClassifyCause(panicErr))
}

// ── T-15: FaultChain returns all faults outermost-first ──────────────────────

func TestFaultChain_OutermostFirst(t *testing.T) {
	root := errors.New("db error")
	err := Service("Login.tx", Store("GetUser.query", root))

	chain := FaultChain(err)
	require.Len(t, chain, 2)

	assert.Equal(t, "service", chain[0].Layer)
	assert.Equal(t, "Login.tx", chain[0].Op)

	assert.Equal(t, "store", chain[1].Layer)
	assert.Equal(t, "GetUser.query", chain[1].Op)
}

func TestFaultChain_Empty(t *testing.T) {
	assert.Empty(t, FaultChain(errors.New("plain")))
	assert.Empty(t, FaultChain(nil))
}

// ── Fault.Error() formatting ──────────────────────────────────────────────────

func TestFaultError_WithOp(t *testing.T) {
	f := &Fault{Op: "GetUser.query", Layer: LayerStore, Err: errors.New("sql error")}
	assert.Equal(t, "store.GetUser.query: sql error", f.Error())
}

func TestFaultError_WithoutOp(t *testing.T) {
	f := &Fault{Layer: LayerStore, Err: errors.New("sql error")}
	assert.Equal(t, "store: sql error", f.Error())
}

// ── pgx.ErrNoRows → CauseDB ──────────────────────────────────────────────────

func TestClassifyCause_PgxErrNoRows(t *testing.T) {
	assert.Equal(t, CauseDB, ClassifyCause(pgx.ErrNoRows))
}
