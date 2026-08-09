// Package mega owns MEGA protocol, link, cryptography, and integrity adapters.
//
// The pinned go-mega dependency is intentionally limited to its URL-safe
// base64 decoder. Public-link parsing, folder traversal, payload URL
// acquisition, AES-CTR positioning, and integrity verification are local
// implementations because that upstream version does not expose the modern
// public-link APIs.
package mega
