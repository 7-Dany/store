package bootstrap

import "strings"

// validateBootstrapRequest validates the decoded request body in-place.
// Returns an error if bootstrap_secret is absent.
func validateBootstrapRequest(req *bootstrapRequest) error {
	if strings.TrimSpace(req.BootstrapSecret) == "" {
		return ErrBootstrapSecretEmpty
	}
	return nil
}
