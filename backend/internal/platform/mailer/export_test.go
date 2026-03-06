// export_test.go — compile-time only (part of package mailer, not mailer_test).
// Exposes internal knobs needed by black-box tests without polluting the production API.
package mailer

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/google/uuid"
)

// SetTestRetryDelays overrides the package-level retry delay variables for the
// duration of a single test.  The returned function restores the originals and
// must be called via defer:
//
//	defer mailer.SetTestRetryDelays(1*time.Millisecond, 4*time.Millisecond)()
//
// Tests that call this function must NOT run in parallel with each other
// because they mutate shared package state.
func SetTestRetryDelays(base, max time.Duration) (restore func()) {
	origBase, origMax := baseRetryDelay, maxRetryDelay
	baseRetryDelay = base
	maxRetryDelay = max
	return func() {
		baseRetryDelay = origBase
		maxRetryDelay = origMax
	}
}

// ── Template injection ────────────────────────────────────────────────────────
// Template strings live in the mailer/templates subpackage. Tests that need to
// swap them import that package directly and assign to the exported pointer:
//
//	orig := *mailertemplates.VerificationEmailTemplate
//	*mailertemplates.VerificationEmailTemplate = "{{invalid"
//	defer func() { *mailertemplates.VerificationEmailTemplate = orig }()
//
// NewWithCustomTemplate is exported from mailer.go for injecting a bad template
// at construction time to exercise the template.Execute error branch.

// ── UUID injection ────────────────────────────────────────────────────────────

// UUIDNewRandom is a pointer to the package-level uuid generation function.
// Tests may replace it with a function that returns an error to exercise the
// uuid.NewRandom error branch in buildMessage. MUST restore and MUST NOT be
// called in parallel with other tests that mutate this variable.
var UUIDNewRandom = &uuidNewRandom

// RestoreUUIDNewRandom returns a function that restores uuidNewRandom to its
// original value. Intended for use with defer:
//
//	defer mailer.SetTestUUIDNewRandom(failingFunc)()
func SetTestUUIDNewRandom(fn func() (uuid.UUID, error)) (restore func()) {
	orig := uuidNewRandom
	uuidNewRandom = fn
	return func() { uuidNewRandom = orig }
}

// ── Body writer injection ─────────────────────────────────────────────────────

// SetTestBodyWriter overrides the quoted-printable body writer factory used by
// buildMessage. The returned function restores the original. MUST NOT be called
// in parallel with other tests that mutate this variable.
func SetTestBodyWriter(fn func(io.Writer) io.WriteCloser) (restore func()) {
	orig := newBodyWriter
	newBodyWriter = fn
	return func() { newBodyWriter = orig }
}

// ── TCP dial injection ────────────────────────────────────────────────────────

// SetTestDialFunc overrides the TCP dial function used by sendWithContext,
// allowing tests to inject a net.Pipe connection for deterministic I/O.
// The returned function restores the original. MUST NOT be called in parallel
// with other tests that mutate this variable.
func SetTestDialFunc(fn func(ctx context.Context, addr string) (net.Conn, error)) (restore func()) {
	orig := dialFunc
	dialFunc = fn
	return func() { dialFunc = orig }
}

// ── Direct sendWithContext access ─────────────────────────────────────────────

// SendWithContextForTest exposes the unexported sendWithContext function so
// tests can call it with an arbitrarily large synthetic message body.
// This is needed to exercise the wc.Write error branch: net/smtp buffers
// writes internally (bufio, 4096 bytes), so a small real email never flushes
// to the network inside Write — the error only surfaces at wc.Close. Passing
// a message larger than the buffer forces a mid-write flush that fails when
// the server has already closed the connection.
var SendWithContextForTest = sendWithContext
