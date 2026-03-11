package bootstrap

// ValidateBootstrapRequestForTest exposes validateBootstrapRequest for white-box
// testing. Only accessible from _test packages in this directory.
func ValidateBootstrapRequestForTest(secret string) error {
	return validateBootstrapRequest(&bootstrapRequest{BootstrapSecret: secret})
}
