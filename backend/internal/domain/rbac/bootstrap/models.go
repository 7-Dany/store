package bootstrap

// BootstrapInput carries the parsed user_id and request metadata from the handler.
type BootstrapInput struct {
	UserID    [16]byte
	IPAddress string
	UserAgent string
}

// BootstrapUser is an intermediate type used by the store layer to carry the
// fields required for the service-layer guard checks. ID and Email are
// intentionally omitted — the service uses only IsActive and EmailVerified.
type BootstrapUser struct {
	IsActive      bool
	EmailVerified bool
}

// BootstrapTxInput carries the IDs and request metadata needed for the
// transactional owner assignment.
type BootstrapTxInput struct {
	UserID    [16]byte
	RoleID    [16]byte
	IPAddress string
	UserAgent string
}
