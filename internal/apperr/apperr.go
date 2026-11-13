// Package apperr carries an HTTP status and a stable error code from the
// layer that knows what went wrong to the handler that renders it.
package apperr

import (
	"errors"
	"net/http"
)

// Error is rendered as {"error": Code, "message": Message} with Status.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func New(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// As unwraps err into an *Error.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Invalid is a 400 invalid_request.
func Invalid(msg string) *Error { return New(http.StatusBadRequest, "invalid_request", msg) }

var (
	// ErrUnauthenticated: no (valid) panel session.
	ErrUnauthenticated = New(http.StatusUnauthorized, "unauthenticated", "sign in to continue")
	// ErrSessionExpired: the upstream session can no longer be refreshed.
	ErrSessionExpired = New(http.StatusUnauthorized, "session_expired", "your session has ended; sign in again")
	ErrNotAdmin       = New(http.StatusForbidden, "not_admin", "this account is not an administrator")
	ErrCSRF           = New(http.StatusForbidden, "csrf", "missing or invalid CSRF token")
	ErrNotFound       = New(http.StatusNotFound, "not_found", "not found")
	ErrUpstream       = New(http.StatusBadGateway, "upstream_unavailable", "the auth service is unreachable")
)
