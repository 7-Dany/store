package watch

// White-box export shim for test packages.
// Symbols exported here are visible to external test packages (package watch_test)
// but are not part of the package's public API — they exist solely to enable
// direct unit testing of unexported logic without duplicating it.

// SetKey exposes the setKey Redis key helper for integration tests that need to
// read or seed watch-address SET keys directly.
var SetKey = setKey

// RegAtKey exposes the regAtKey Redis key helper so integration tests can seed
// the registration timestamp (e.g. to test the 7-day expiry path in RunWatchCap).
var RegAtKey = regAtKey

// LastActiveKey exposes lastActiveKey for integration tests.
var LastActiveKey = lastActiveKey
