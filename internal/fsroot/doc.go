// Package fsroot owns Linux root-confined filesystem access. It uses held
// directory descriptors and descriptor-relative operations so validation is
// never separated from the security-sensitive filesystem use.
package fsroot
