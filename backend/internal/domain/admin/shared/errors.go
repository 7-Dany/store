package adminshared

import "errors"

// ErrScopeNotAllowed is returned when a permission grant specifies a scope
// value that the permission's scope_policy does not permit.
var ErrScopeNotAllowed = errors.New("scope is not permitted for this permission")
