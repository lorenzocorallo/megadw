package mega

import (
	"bytes"
	"testing"
)

func TestIntegrityVector(t *testing.T) {
	plaintext := integrityTestPlaintext()
	key, err := DecodeFileKey("ABEiM0RVZnepR9BIBCj9ORAgMEBQYHCAsX7KMxgV48Y")
	if err != nil {
		t.Fatal(err)
	}
	if got := key.MetaMAC; got != [8]byte{0xb1, 0x7e, 0xca, 0x33, 0x18, 0x15, 0xe3, 0xc6} {
		t.Fatalf("decoded meta MAC = %x", got)
	}
	if err := VerifyIntegrity(bytes.NewReader(plaintext), int64(len(plaintext)), key); err != nil {
		t.Fatalf("VerifyIntegrity() error = %v", err)
	}

	corrupted := append([]byte(nil), plaintext...)
	corrupted[len(corrupted)/2] ^= 1
	if err := VerifyIntegrity(bytes.NewReader(corrupted), int64(len(corrupted)), key); err != ErrIntegrityMismatch {
		t.Fatalf("corrupted VerifyIntegrity() error = %v, want ErrIntegrityMismatch", err)
	}
}

func integrityTestPlaintext() []byte {
	plain := make([]byte, 5_000_123)
	for index := range plain {
		plain[index] = byte((index*37 + index/31 + 19) & 0xff)
	}
	return plain
}
