package watch

// WatchRequest is the HTTP request body for POST /bitcoin/watch.
type WatchRequest struct {
	Network   string   `json:"network"`
	Addresses []string `json:"addresses"`
}

// WatchResponse is the HTTP response body for a successful POST /bitcoin/watch.
//
// watching contains the normalised addresses from the current request.
// It does NOT represent the user's complete watch list.
type WatchResponse struct {
	Watching []string `json:"watching"`
}

// watchLimitExceededBody is the error response body for the watch_limit_exceeded
// case when the reason is registration_window_expired. The standard respond.Error
// body does not carry a reason field, so this struct is serialised directly.
type watchLimitExceededBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Reason disambiguates the two watch_limit_exceeded causes so the client
	// can display appropriate guidance.
	//   "count_cap"                   — user has too many addresses registered
	//   "registration_window_expired" — 7-day window has lapsed; requires admin reset
	Reason string `json:"reason"`
}
