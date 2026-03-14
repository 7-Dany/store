package templates

// Entry bundles everything a single email type owns: the key used to look it
// up, the subject format string (passed to fmt.Sprintf with AppName), and a
// pointer to the raw HTML template string. HTML is a pointer so tests can swap
// the string to exercise template-parse / execute error paths without touching
// the mailer infrastructure.
type Entry struct {
	Key        string
	SubjectFmt string // e.g. "Verify your %s account"
	HTML       *string
}

// Registry returns every registered email type. NewWithAuth iterates this map
// and parses each entry — it has no knowledge of individual types.
//
// To add a new email type: create a new file in this package with a Key const,
// an HTML template var, and add one Entry here. Nothing in the mailer package
// itself needs to change.
func Registry() map[string]Entry {
	return map[string]Entry{
		VerificationKey: {
			Key:        VerificationKey,
			SubjectFmt: "Verify your %s account",
			HTML:       VerificationEmailTemplate,
		},
		UnlockKey: {
			Key:        UnlockKey,
			SubjectFmt: "Unlock your %s account",
			HTML:       UnlockEmailTemplate,
		},
		PasswordResetKey: {
			Key:        PasswordResetKey,
			SubjectFmt: "Reset your %s password",
			HTML:       PasswordResetEmailTemplate,
		},
		EmailChangeOTPKey: {
			Key:        EmailChangeOTPKey,
			SubjectFmt: "Your %s email change request",
			HTML:       EmailChangeOTPTemplate,
		},
		EmailChangeConfirmOTPKey: {
			Key:        EmailChangeConfirmOTPKey,
			SubjectFmt: "Confirm your new %s email",
			HTML:       EmailChangeConfirmOTPTemplate,
		},
		EmailChangedNotificationKey: {
			Key:        EmailChangedNotificationKey,
			SubjectFmt: "Your %s email address has been changed",
			HTML:       EmailChangedNotificationTemplate,
		},
		AccountDeletionOTPKey: {
			Key:        AccountDeletionOTPKey,
			SubjectFmt: "Delete your %s account",
			HTML:       AccountDeletionOTPTemplate,
		},
		OwnerTransferKey: {
			Key:        OwnerTransferKey,
			SubjectFmt: "You have been invited to become the %s owner",
			HTML:       OwnerTransferEmailTemplate,
		},
	}
}
