package userlock

import "strings"

func validateLockUser(in LockUserInput) error {
	if strings.TrimSpace(in.Reason) == "" {
		return ErrReasonRequired
	}
	return nil
}
