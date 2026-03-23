// Package bitcoinsharedtest provides test-only helpers shared across all
// Bitcoin domain feature sub-packages. It must never be imported by production code.
//
// # Import rules for test files
//
// This package imports watch (and other bitcoin domain sub-packages) to provide
// shared fakes. That is safe for EXTERNAL test packages (package foo_test) which
// are compiled in their own binary and are exempt from the standard cycle rules.
//
// It must NOT be imported by INTERNAL test files (package foo, not package foo_test)
// because those files are compiled as part of the foo package itself, closing the
// cycle:  foo → bitcoinsharedtest → foo.
//
// Pattern:
//   handler_test.go  (package watch_test) — may import bitcoinsharedtest ✓
//   service_test.go  (package watch)      — must NOT import bitcoinsharedtest ✗
package bitcoinsharedtest
