package authshared

import "sync"

// ResetDummyOTPHashForTest resets the package-level sync.Once and cached value
// so that the next call to GetDummyOTPHash re-executes the initialisation.
// Only available during testing (export_test.go is excluded from production builds).
func ResetDummyOTPHashForTest() {
	dummyOTPHashOnce = sync.Once{}
	dummyOTPHashValue = ""
}

// ResetDummyPasswordHashForTest resets the package-level sync.Once and cached
// value so that the next call to GetDummyPasswordHash re-executes the
// initialisation. Call SetBcryptCostForTest (or SetBcryptCostUnsafeForTest)
// before the next GetDummyPasswordHash to control which cost is used.
// Only available during testing (export_test.go is excluded from production builds).
func ResetDummyPasswordHashForTest() {
	dummyPasswordHashOnce = sync.Once{}
	dummyPasswordHashValue = ""
}
