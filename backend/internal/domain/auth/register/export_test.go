package register

// SetGenerateCodeHashForTest replaces the generateCodeHash function for the
// duration of a test. Call only from test code; the field is unexported in
// production.
func (s *Service) SetGenerateCodeHashForTest(fn func() (string, string, error)) {
	s.generateCodeHash = fn
}

// ExportedValidateAndNormalise exposes the unexported validateAndNormalise
// function so external test packages (package register_test) can exercise
// every validation and normalisation branch directly.
func ExportedValidateAndNormalise(req *registerRequest) error {
	return validateAndNormalise(req)
}

// ExportedRegisterRequest constructs a registerRequest value for use in
// external test packages. The type is unexported; callers use := for
// type inference.
func ExportedRegisterRequest(displayName, email, password string) registerRequest {
	return registerRequest{DisplayName: displayName, Email: email, Password: password}
}
