package owner

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Storer is the data-access contract for the owner service.
type Storer interface {
	// Assign-owner path.
	CountActiveOwners(ctx context.Context) (int64, error)
	GetOwnerRoleID(ctx context.Context) ([16]byte, error)
	GetActiveUserByID(ctx context.Context, userID [16]byte) (AssignOwnerUser, error)
	AssignOwnerTx(ctx context.Context, in AssignOwnerTxInput) (AssignOwnerResult, error)

	// Transfer path.
	GetTransferTargetUser(ctx context.Context, userID [16]byte) (TransferTargetUser, error)
	HasPendingTransferToken(ctx context.Context) (bool, error)
	InsertTransferToken(ctx context.Context, targetUserID [16]byte, targetEmail, codeHash, initiatedBy string) ([16]byte, time.Time, error)
	GetPendingTransferToken(ctx context.Context) (PendingTransferInfo, error)
	DeletePendingTransferToken(ctx context.Context, initiatedBy string) error
	WriteInitiateAuditLog(ctx context.Context, actingOwnerID [16]byte, targetUserID, ipAddress, userAgent string) error
	WriteCancelAuditLog(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error
	AcceptTransferTx(ctx context.Context, in AcceptTransferTxInput) (time.Time, error)
}

// Service implements Servicer.
type Service struct {
	store Storer
}

// NewService constructs a Service.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// compile-time check: Service satisfies Servicer.
var _ Servicer = (*Service)(nil)

// transferTokenBcryptCost is the bcrypt work factor used by generateTransferToken.
// Lowered to bcrypt.MinCost in tests via SetTransferTokenBcryptCostForTest.
var transferTokenBcryptCost = bcrypt.DefaultCost

// ── AssignOwner ───────────────────────────────────────────────────────────────

// AssignOwner assigns the owner role to the authenticated user identified by
// in.UserID. This is the one-time bootstrap path — it fails if an active owner
// already exists or if the account is not yet active and email-verified.
func (s *Service) AssignOwner(ctx context.Context, in AssignOwnerInput) (AssignOwnerResult, error) {
	// 1. Enforce single-owner invariant.
	count, err := s.store.CountActiveOwners(ctx)
	if err != nil {
		return AssignOwnerResult{}, fmt.Errorf("service.AssignOwner: count owners: %w", err)
	}
	if count > 0 {
		return AssignOwnerResult{}, ErrOwnerAlreadyExists
	}

	// 2. Resolve the owner role ID.
	roleID, err := s.store.GetOwnerRoleID(ctx)
	if err != nil {
		return AssignOwnerResult{}, fmt.Errorf("service.AssignOwner: get owner role: %w", err)
	}

	// 3. Fetch and validate the calling user.
	user, err := s.store.GetActiveUserByID(ctx, in.UserID)
	if err != nil {
		return AssignOwnerResult{}, fmt.Errorf("service.AssignOwner: get user: %w", err)
	}
	if !user.IsActive {
		return AssignOwnerResult{}, ErrUserNotActive
	}
	if !user.EmailVerified {
		return AssignOwnerResult{}, ErrUserNotVerified
	}

	// 4. Assign the owner role transactionally.
	result, err := s.store.AssignOwnerTx(ctx, AssignOwnerTxInput{
		UserID:    in.UserID,
		RoleID:    roleID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
	})
	if err != nil {
		return AssignOwnerResult{}, fmt.Errorf("service.AssignOwner: tx: %w", err)
	}
	return result, nil
}

// ── HasPendingTransfer ───────────────────────────────────────────────────────

// HasPendingTransfer reports whether there is currently an unexpired,
// unconsumed transfer token in the store. It is a thin wrapper around
// store.HasPendingTransferToken, exposed on the Servicer interface so the
// handler can perform the idempotency check before the rate limiter fires.
func (s *Service) HasPendingTransfer(ctx context.Context) (bool, error) {
	return s.store.HasPendingTransferToken(ctx)
}

// ── InitiateTransfer ──────────────────────────────────────────────────────────

// InitiateTransfer validates the target user, checks for an existing pending
// transfer, generates a raw 32-byte transfer token, stores its bcrypt hash,
// and writes the initiate audit log. Returns the result, the raw token (for
// emailing to the target), and any error.
func (s *Service) InitiateTransfer(ctx context.Context, in InitiateInput) (InitiateResult, string, error) {
	// Guard: cannot transfer to self.
	if in.ActingOwnerID == in.TargetUserID {
		return InitiateResult{}, "", ErrCannotTransferToSelf
	}

	// Guard: one pending transfer at a time.
	pending, err := s.store.HasPendingTransferToken(ctx)
	if err != nil {
		return InitiateResult{}, "", fmt.Errorf("service.InitiateTransfer: check pending: %w", err)
	}
	if pending {
		return InitiateResult{}, "", ErrTransferAlreadyPending
	}

	// Validate target user eligibility.
	target, err := s.store.GetTransferTargetUser(ctx, in.TargetUserID)
	if err != nil {
		return InitiateResult{}, "", fmt.Errorf("service.InitiateTransfer: get target: %w", err)
	}
	if target.IsOwner {
		return InitiateResult{}, "", ErrUserIsAlreadyOwner
	}
	if !target.IsActive {
		return InitiateResult{}, "", ErrUserNotActive
	}
	if !target.EmailVerified {
		return InitiateResult{}, "", ErrUserNotVerified
	}

	// Generate raw token + bcrypt hash.
	rawToken, codeHash, err := generateTransferToken()
	if err != nil {
		return InitiateResult{}, "", fmt.Errorf("service.InitiateTransfer: generate token: %w", err)
	}

	ownerIDStr := uuid.UUID(in.ActingOwnerID).String()
	targetIDStr := uuid.UUID(in.TargetUserID).String()

	// Persist hashed token.
	tokenID, expiresAt, err := s.store.InsertTransferToken(ctx, in.TargetUserID, target.Email, codeHash, ownerIDStr)
	if err != nil {
		return InitiateResult{}, "", fmt.Errorf("service.InitiateTransfer: insert token: %w", err)
	}

	// Audit log (non-fatal — token is already committed).
	if err := s.store.WriteInitiateAuditLog(ctx, in.ActingOwnerID, targetIDStr, in.IPAddress, in.UserAgent); err != nil {
		slog.ErrorContext(ctx, "owner.InitiateTransfer: audit log", "error", err)
	}

	return InitiateResult{
		TransferID:   uuid.UUID(tokenID).String(),
		TargetUserID: targetIDStr,
		TargetEmail:  target.Email,
		ExpiresAt:    expiresAt,
	}, rawToken, nil
}

// ── AcceptTransfer ────────────────────────────────────────────────────────────

// AcceptTransfer validates and consumes the raw token, re-checks target
// eligibility, resolves role IDs, and delegates the atomic role swap to
// AcceptTransferTx.
func (s *Service) AcceptTransfer(ctx context.Context, in AcceptInput) (AcceptResult, error) {
	// Fetch the pending token row (FOR UPDATE to prevent concurrent accepts).
	info, err := s.store.GetPendingTransferToken(ctx)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("service.AcceptTransfer: get token: %w", err)
	}

	// Verify the raw token against the stored bcrypt hash.
	if err := bcrypt.CompareHashAndPassword([]byte(info.CodeHash), []byte(in.RawToken)); err != nil {
		return AcceptResult{}, ErrTransferTokenInvalid
	}

	// Parse the initiating owner's UUID.
	previousOwnerID, err := parseUUID16(info.InitiatedBy)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("service.AcceptTransfer: parse initiator uuid: %w", err)
	}

	// Re-validate the target (new owner) at accept time.
	target, err := s.store.GetTransferTargetUser(ctx, info.NewOwnerID)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("service.AcceptTransfer: re-check target: %w", err)
	}
	if !target.IsActive || !target.EmailVerified {
		return AcceptResult{}, ErrUserNotEligible
	}

	// Resolve owner role ID.
	roleID, err := s.store.GetOwnerRoleID(ctx)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("service.AcceptTransfer: get owner role: %w", err)
	}

	// Security: detach from the request context so a client-timed disconnect cannot
	// abort this irreversible role swap mid-transaction (ADR-004).
	transferredAt, err := s.store.AcceptTransferTx(context.WithoutCancel(ctx), AcceptTransferTxInput{
		TokenID:         info.TokenID,
		NewOwnerID:      info.NewOwnerID,
		PreviousOwnerID: previousOwnerID,
		RoleID:          roleID,
		ActingUserID:    info.NewOwnerID,
		IPAddress:       in.IPAddress,
		UserAgent:       in.UserAgent,
	})
	if err != nil {
		return AcceptResult{}, fmt.Errorf("service.AcceptTransfer: tx: %w", err)
	}

	return AcceptResult{
		NewOwnerID:      uuid.UUID(info.NewOwnerID).String(),
		PreviousOwnerID: uuid.UUID(previousOwnerID).String(),
		TransferredAt:   transferredAt,
	}, nil
}

// ── CancelTransfer ────────────────────────────────────────────────────────────

// CancelTransfer deletes the pending transfer token initiated by actingOwnerID.
// Returns ErrNoPendingTransfer if none exists.
func (s *Service) CancelTransfer(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error {
	ownerIDStr := uuid.UUID(actingOwnerID).String()
	if err := s.store.DeletePendingTransferToken(ctx, ownerIDStr); err != nil {
		return fmt.Errorf("service.CancelTransfer: %w", err)
	}
	// Audit log (non-fatal).
	if err := s.store.WriteCancelAuditLog(ctx, actingOwnerID, ipAddress, userAgent); err != nil {
		slog.ErrorContext(ctx, "owner.CancelTransfer: audit log", "error", err)
	}
	return nil
}

// ── Token helpers ─────────────────────────────────────────────────────────────

// generateTransferToken creates a cryptographically random 32-byte raw token
// (base64url-encoded, no padding) and its bcrypt hash.
func generateTransferToken() (rawToken, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generateTransferToken: rand: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	h, err := bcrypt.GenerateFromPassword([]byte(raw), transferTokenBcryptCost)
	if err != nil {
		return "", "", fmt.Errorf("generateTransferToken: bcrypt: %w", err)
	}
	return raw, string(h), nil
}

// parseUUID16 parses a UUID string into a [16]byte.
func parseUUID16(s string) ([16]byte, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return [16]byte{}, fmt.Errorf("parseUUID16: %w", err)
	}
	return [16]byte(u), nil
}
