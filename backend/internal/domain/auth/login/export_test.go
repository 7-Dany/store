// This file is compiled only during `go test`. It exposes unexported identifiers
// so external test packages (package auth_test) can verify internal behaviour.
// It must never be imported by production code.

package login

// LoginRequestView holds the normalised login fields returned by
// ValidateLoginForTest.
type LoginRequestView struct {
	Identifier string
	Password   string
}

// ValidateLoginForTest calls validateLoginRequest and returns a view of the
// normalised request struct.
func ValidateLoginForTest(identifier, password string) (LoginRequestView, error) {
	req := &loginRequest{Identifier: identifier, Password: password}
	if err := validateLoginRequest(req); err != nil {
		return LoginRequestView{}, err
	}
	return LoginRequestView{
		Identifier: req.Identifier,
		Password:   req.Password,
	}, nil
}
