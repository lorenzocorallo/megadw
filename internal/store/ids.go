package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
