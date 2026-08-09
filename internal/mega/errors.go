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
)

// APIError is an error returned by the MEGA command endpoint.
type APIError struct {
	Code int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("MEGA API error %d", e.Code)
}
