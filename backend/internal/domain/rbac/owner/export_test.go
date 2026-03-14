package owner

import "context"

// ── Validator exports ─────────────────────────────────────────────────────────

// ValidateAssignOwnerRequest exposes the internal validator for unit tests.
func ValidateAssignOwnerRequest(secret string) error {
	return validateAssignOwnerRequest(&assignOwnerRequest{Secret: secret})
}

// ValidateInitiateRequest exposes the internal validator for unit tests.
func ValidateInitiateRequest(targetUserID string) error {
	return validateInitiateRequest(&initiateRequest{TargetUserID: targetUserID})
}

// ValidateAcceptRequest exposes the internal validator for unit tests.
func ValidateAcceptRequest(rawToken string) error {
	return validateAcceptRequest(&acceptRequest{Token: rawToken})
}

// Exported unexported validation sentinels for ErrorIs checks in tests.
var (
	ErrTargetUserIDRequired = errTargetUserIDRequired
	ErrTargetUserIDInvalid  = errTargetUserIDInvalid
	ErrTokenRequired        = errTokenRequired
)

// ── Handler test helpers ──────────────────────────────────────────────────────

// FakeOwnerDeps is a handlerDeps test double for use in handler unit tests.
// It is defined here (package owner) so it can implement the unexported
// handlerDeps interface. The exported fields allow handler_test.go to control
// the isOwner response per-test.
type FakeOwnerDeps struct {
	IsOwnerFn func(ctx context.Context, userID string) (bool, error)
}

func (f *FakeOwnerDeps) isOwner(ctx context.Context, userID string) (bool, error) {
	if f.IsOwnerFn != nil {
		return f.IsOwnerFn(ctx, userID)
	}
	return true, nil // default: caller is owner
}

// NewHandlerForTest constructs a Handler with a directly injected handlerDeps.
// Used in unit tests to bypass the rbac.Checker wiring in NewHandler.
// mailer and mailQueue are nil — no email is sent in unit tests.
func NewHandlerForTest(svc Servicer, secret string, deps *FakeOwnerDeps) *Handler {
	return &Handler{svc: svc, secret: secret, deps: deps, mailer: nil, mailQueue: nil}
}

// SetTransferTokenBcryptCostForTest lowers the bcrypt cost used by
// generateTransferToken for fast unit tests.
func SetTransferTokenBcryptCostForTest(cost int) {
	transferTokenBcryptCost = cost
}
