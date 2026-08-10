// Package mega owns MEGA protocol, link, cryptography, and integrity adapters.
//
// The pinned go-mega dependency is intentionally limited to its URL-safe
// base64 decoder. Public-link parsing, folder traversal, payload URL
// acquisition, AES-CTR positioning, integrity verification, and the
// authentication-only account lifecycle are local implementations. The
// upstream full-client login is not used because it loads the private tree and
// starts an unowned event poller.
package mega
