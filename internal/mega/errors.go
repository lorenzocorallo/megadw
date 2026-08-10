package mega

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidLink identifies a malformed or unsupported public MEGA link.
	ErrInvalidLink = errors.New("invalid MEGA public link")
	// ErrInvalidKey identifies malformed public-link key material.
	ErrInvalidKey = errors.New("invalid MEGA key")
	// ErrInvalidAttributes identifies an attribute block that cannot be decrypted or parsed.
	ErrInvalidAttributes = errors.New("invalid MEGA attributes")
	// ErrIntegrityMismatch identifies a plaintext file whose condensed MAC does not match its key.
	ErrIntegrityMismatch = errors.New("MEGA file integrity check failed")
	// ErrSessionRejected identifies a stored session that MEGA no longer accepts.
	ErrSessionRejected = errors.New("MEGA session rejected")
	// ErrReauthRequired is stable application-facing state for an account that
	// has no reusable encrypted password after its session was rejected.
	ErrReauthRequired = errors.New("MEGA account requires authentication")
	// ErrMFAUnsupported is deliberately explicit. The MVP never attempts to
	// bypass or weaken MFA/2FA protection.
	ErrMFAUnsupported = errors.New("MFA-enabled MEGA accounts are not supported in this version")
	// ErrAuthenticationFailed is returned for a bounded password-auth failure
	// without exposing upstream protocol details or credentials.
	ErrAuthenticationFailed = errors.New("MEGA account authentication failed")
)

// APIError is an error returned by the MEGA command endpoint.
type APIError struct {
	Code int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("MEGA API error %d", e.Code)
}

func (e *APIError) Is(target error) bool {
	return e != nil && e.Code == -15 && target == ErrSessionRejected
}

// APIHTTPError carries only the response status. It intentionally omits the
// request URL because authenticated MEGA URLs contain the opaque session ID.
type APIHTTPError struct {
	StatusCode int
}

func (e *APIHTTPError) Error() string {
	if e == nil {
		return "MEGA API HTTP request failed"
	}
	return fmt.Sprintf("MEGA API HTTP status %d", e.StatusCode)
}
