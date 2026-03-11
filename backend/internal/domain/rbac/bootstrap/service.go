package bootstrap

import (
	"context"
	"fmt"

	"github.com/7-Dany/store/backend/internal/platform/rbac"
)

// Storer is the data-access contract for the bootstrap service.
type Storer interface {
	CountActiveOwners(ctx context.Context) (int64, error)
	GetOwnerRoleID(ctx context.Context) ([16]byte, error)
	GetActiveUserByID(ctx context.Context, userID [16]byte) (BootstrapUser, error)
	BootstrapOwnerTx(ctx context.Context, in BootstrapTxInput) (BootstrapResult, error)
}

// Service implements Servicer.
type Service struct {
	store Storer
}

// NewService constructs a Service.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// Bootstrap assigns the owner role to the user identified by in.UserID.
// It fails fast if an active owner already exists, if the user cannot be
// found, or if the account is not yet active/verified.
func (s *Service) Bootstrap(ctx context.Context, in BootstrapInput) (BootstrapResult, error) {
	// 1. Enforce single-owner invariant.
	count, err := s.store.CountActiveOwners(ctx)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("service.Bootstrap: count owners: %w", err)
	}
	if count > 0 {
		return BootstrapResult{}, rbac.ErrOwnerAlreadyExists
	}

	// 2. Fetch the owner role ID.
	roleID, err := s.store.GetOwnerRoleID(ctx)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("service.Bootstrap: get owner role: %w", err)
	}

	// 3. Fetch and validate the target user.
	user, err := s.store.GetActiveUserByID(ctx, in.UserID)
	if err != nil {
		// rbacshared.ErrUserNotFound is wrapped with %w, so errors.Is in the
		// handler's switch will find it through the wrapping.
		return BootstrapResult{}, fmt.Errorf("service.Bootstrap: get user: %w", err)
	}
	if !user.IsActive {
		return BootstrapResult{}, ErrUserNotActive
	}
	if !user.EmailVerified {
		return BootstrapResult{}, ErrUserNotVerified
	}

	// 4. Assign the owner role transactionally.
	result, err := s.store.BootstrapOwnerTx(ctx, BootstrapTxInput{
		UserID:    in.UserID,
		RoleID:    roleID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
	})
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("service.Bootstrap: tx: %w", err)
	}

	return result, nil
}

// compile-time check: Service satisfies Servicer.
var _ Servicer = (*Service)(nil)
