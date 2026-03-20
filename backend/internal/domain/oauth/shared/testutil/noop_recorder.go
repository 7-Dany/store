package oauthsharedtest

// NoopOAuthRecorder satisfies both google.Recorder and telegram.Recorder
// (and any other OAuth handler Recorder interface) with empty method bodies.
// Use it in handler unit tests that do not need metric assertions.
type NoopOAuthRecorder struct{}

func (NoopOAuthRecorder) OnOAuthSuccess(string)          {}
func (NoopOAuthRecorder) OnOAuthFailed(string, string)   {}
func (NoopOAuthRecorder) OnOAuthLinked(string)           {}
func (NoopOAuthRecorder) OnOAuthUnlinked(string)         {}
