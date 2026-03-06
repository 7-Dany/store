// This file is compiled only during `go test`. It exposes unexported identifiers
// so external test packages (package verification_test) can verify internal behaviour.
// It must never be imported by production code.

package verification

// VerifyEmailRequestView holds the normalised fields returned by
// ValidateVerifyEmailForTest.
type VerifyEmailRequestView struct {
	Email string
	Code  string
}

// ResendRequestView holds the normalised fields returned by
// ValidateResendForTest.
type ResendRequestView struct {
	Email string
}

// ValidateVerifyEmailForTest calls validateVerifyEmailRequest and returns a
// view of the normalised request struct.
func ValidateVerifyEmailForTest(email, code string) (VerifyEmailRequestView, error) {
	req := &verifyEmailRequest{Email: email, Code: code}
	if err := validateVerifyEmailRequest(req); err != nil {
		return VerifyEmailRequestView{}, err
	}
	return VerifyEmailRequestView{Email: req.Email, Code: req.Code}, nil
}

// ValidateResendForTest calls validateResendRequest and returns a view of the
// normalised request struct.
func ValidateResendForTest(email string) (ResendRequestView, error) {
	req := &resendVerificationRequest{Email: email}
	if err := validateResendRequest(req); err != nil {
		return ResendRequestView{}, err
	}
	return ResendRequestView{Email: req.Email}, nil
}
