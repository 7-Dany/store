package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockNetError implements net.Error for testing classifyError.
type mockNetError struct {
	timeout bool
}

func (e *mockNetError) Error() string   { return "mock net error" }
func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return false }

// ── IsNotFoundError ───────────────────────────────────────────────────────────

func TestIsNotFoundError_RPCErrorCode5_ReturnsTrue(t *testing.T) {
	err := &RPCError{Code: -5, Message: "No such wallet transaction"}
	assert.True(t, IsNotFoundError(err))
}

func TestIsNotFoundError_RPCErrorCode5_MempoolMessage_ReturnsTrue(t *testing.T) {
	err := &RPCError{Code: -5, Message: "Transaction not in mempool"}
	assert.True(t, IsNotFoundError(err))
}

func TestIsNotFoundError_OtherRPCCode_ReturnsFalse(t *testing.T) {
	err := &RPCError{Code: -8, Message: "Invalid parameter"}
	assert.False(t, IsNotFoundError(err))
}

func TestIsNotFoundError_NonRPCError_ReturnsFalse(t *testing.T) {
	err := errors.New("connection refused")
	assert.False(t, IsNotFoundError(err))
}

func TestIsNotFoundError_Nil_ReturnsFalse(t *testing.T) {
	assert.False(t, IsNotFoundError(nil))
}

// ── IsPrunedBlockError ────────────────────────────────────────────────────────

func TestIsPrunedBlockError_PrunedData_ReturnsTrue(t *testing.T) {
	err := &RPCError{Code: -1, Message: "Block not found on disk: pruned data"}
	assert.True(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_BlockNotAvailable_ReturnsTrue(t *testing.T) {
	err := errors.New("Block not available (pruned data)")
	assert.True(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_UnrelatedError_ReturnsFalse(t *testing.T) {
	err := errors.New("Block not found")
	assert.False(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_Nil_ReturnsFalse(t *testing.T) {
	assert.False(t, IsPrunedBlockError(nil))
}

func TestIsPrunedBlockError_WrappedRPCError_ReturnsTrue(t *testing.T) {
	err := fmt.Errorf("wrapper: %w", &RPCError{Code: -1, Message: "pruned data"})
	assert.True(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_RPCErrorCodeMinus1WrongMessage_ReturnsFalse(t *testing.T) {
	err := &RPCError{Code: -1, Message: "Internal error"}
	assert.False(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_RPCErrorCodeOther_ReturnsFalse(t *testing.T) {
	err := &RPCError{Code: -5, Message: "pruned data somewhere"}
	assert.False(t, IsPrunedBlockError(err))
}

// ── RPCError ──────────────────────────────────────────────────────────────────

func TestRPCError_Error_IncludesCodeAndMessage(t *testing.T) {
	err := &RPCError{Code: -5, Message: "No such wallet transaction"}
	assert.Contains(t, err.Error(), "-5")
	assert.Contains(t, err.Error(), "No such wallet transaction")
}

// ── classifyError ─────────────────────────────────────────────────────────────

func TestClassifyError_NotFound(t *testing.T) {
	err := &RPCError{Code: -5, Message: "No such wallet transaction"}
	assert.Equal(t, RPCErrNotFound, classifyError(err))
}

func TestClassifyError_Pruned(t *testing.T) {
	err := errors.New("Block not available (pruned data)")
	assert.Equal(t, RPCErrPruned, classifyError(err))
}

func TestClassifyError_RPCError(t *testing.T) {
	err := &RPCError{Code: -25, Message: "Insufficient funds"}
	assert.Equal(t, RPCErrRPC, classifyError(err))
}

func TestClassifyError_Timeout(t *testing.T) {
	assert.Equal(t, RPCErrTimeout, classifyError(context.DeadlineExceeded))
}

func TestClassifyError_Canceled(t *testing.T) {
	assert.Equal(t, RPCErrCanceled, classifyError(context.Canceled))
}

func TestClassifyError_Nil(t *testing.T) {
	assert.Equal(t, RPCErrType(""), classifyError(nil))
}

func TestClassifyError_Unknown(t *testing.T) {
	assert.Equal(t, RPCErrUnknown, classifyError(errors.New("marshal failure")))
}

func TestClassifyError_EOF_IsNetwork(t *testing.T) {
	assert.Equal(t, RPCErrNetwork, classifyError(io.EOF))
}

func TestClassifyError_UnexpectedEOF_IsNetwork(t *testing.T) {
	assert.Equal(t, RPCErrNetwork, classifyError(io.ErrUnexpectedEOF))
}

func TestClassifyError_HTTPStatusError_IsNetwork(t *testing.T) {
	err := &httpStatusError{StatusCode: http.StatusServiceUnavailable}
	assert.Equal(t, RPCErrNetwork, classifyError(err))

	err = &httpStatusError{StatusCode: http.StatusBadGateway}
	assert.Equal(t, RPCErrNetwork, classifyError(err))

	err = &httpStatusError{StatusCode: http.StatusTooManyRequests}
	assert.Equal(t, RPCErrNetwork, classifyError(err))
}

func TestClassifyError_WrappedHTTPStatusError_IsNetwork(t *testing.T) {
	err := fmt.Errorf("wrapper: %w", &httpStatusError{StatusCode: 503})
	assert.Equal(t, RPCErrNetwork, classifyError(err))
}

func TestClassifyError_NetErrorTimeout_IsTimeout(t *testing.T) {
	err := &mockNetError{timeout: true}
	assert.Equal(t, RPCErrTimeout, classifyError(err))
}

func TestClassifyError_NetErrorNoTimeout_IsNetwork(t *testing.T) {
	err := &mockNetError{timeout: false}
	assert.Equal(t, RPCErrNetwork, classifyError(err))
}

func TestClassifyError_ContextCanceled_TakesPrecedenceOverRPCError(t *testing.T) {
	t.Parallel()
	err := errors.Join(context.Canceled, &RPCError{Code: -5, Message: "not found"})
	assert.Equal(t, RPCErrCanceled, classifyError(err))
}

func TestClassifyError_ContextDeadline_TakesPrecedenceOverNetwork(t *testing.T) {
	t.Parallel()
	err := errors.Join(context.DeadlineExceeded, io.EOF)
	assert.Equal(t, RPCErrTimeout, classifyError(err))
}

// ── Fuzz targets ──────────────────────────────────────────────────────────────

func FuzzClassifyError(f *testing.F) {
	// Seed with diverse error types to exercise all classifyError branches.
	f.Add(-5, "not found")
	f.Add(-1, "pruned data")
	f.Add(-25, "insufficient funds")
	f.Add(0, "")
	f.Add(-18, "no wallet")

	f.Fuzz(func(t *testing.T, code int, msg string) {
		// Should never panic.
		err := &RPCError{Code: code, Message: msg}
		_ = classifyError(err)

		// Also test wrapped errors.
		wrapped := fmt.Errorf("wrapper: %w", err)
		_ = classifyError(wrapped)
	})
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

func BenchmarkClassifyError(b *testing.B) {
	err := &RPCError{Code: -5, Message: "No such wallet transaction"}
	for i := 0; i < b.N; i++ {
		_ = classifyError(err)
	}
}
