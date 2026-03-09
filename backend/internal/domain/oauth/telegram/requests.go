// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

// telegramCallbackRequest is the JSON body posted by the Telegram Login Widget.
// Used for both POST /callback and POST /link.
//
// All fields except ID, AuthDate, and Hash are optional — Telegram does not
// guarantee their presence.
type telegramCallbackRequest struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	PhotoURL  string `json:"photo_url"`
	AuthDate  int64  `json:"auth_date"`
	Hash      string `json:"hash"`
}
