package username

// availableResponse is the JSON body returned by GET /username/available.
type availableResponse struct {
	Available bool `json:"available"`
}

// updateUsernameRequest is the JSON body for PATCH /me/username.
type updateUsernameRequest struct {
	Username string `json:"username"`
}

// updateUsernameResponse is the JSON body returned on a successful username update.
type updateUsernameResponse struct {
	Message string `json:"message"`
}
