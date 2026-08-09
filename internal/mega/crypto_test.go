package mega

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestDecodeFileKeyVector(t *testing.T) {
	key, err := DecodeFileKey("ABEiM0RVZneImaq7zN3u_xAgMEBQYHCAkKCwwNDg8AA")
	if err != nil {
		t.Fatalf("DecodeFileKey() error = %v", err)
	}
	if got := hex.EncodeToString(key.AESKey[:]); got != "10311273143516f718391a7b1c3d1eff" {
		t.Fatalf("AES key = %s", got)
	}
	if got := hex.EncodeToString(key.Nonce[:]); got != "1020304050607080" {
		t.Fatalf("nonce = %s", got)
	}
	if got := hex.EncodeToString(key.MetaMAC[:]); got != "90a0b0c0d0e0f000" {
		t.Fatalf("meta MAC = %s", got)
	}
}

func TestAESCTRVectorAtAlignedAndUnalignedOffsets(t *testing.T) {
	key, err := DecodeFileKey("ABEiM0RVZneImaq7zN3u_xAgMEBQYHCAkKCwwNDg8AA")
	if err != nil {
		t.Fatalf("DecodeFileKey() error = %v", err)
	}
	plaintext := []byte("MEGA Phase B CTR vector: offset-safe!")
	ciphertext, err := hex.DecodeString("90f01fa914a77fa67385800beb96d27cb242e89507b7982f670a4e38f9ea524c04bf3c62f1")
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int64{0, 16, 17, 31} {
		got, err := DecryptAt(ciphertext[offset:], key, offset)
		if err != nil {
			t.Fatalf("DecryptAt(offset=%d) error = %v", offset, err)
		}
		if !bytes.Equal(got, plaintext[offset:]) {
			t.Fatalf("DecryptAt(offset=%d) = %q, want %q", offset, got, plaintext[offset:])
		}
	}
}

func TestDecryptAttributes(t *testing.T) {
	key, err := DecodeFileKey("ABEiM0RVZneImaq7zN3u_xAgMEBQYHCAkKCwwNDg8AA")
	if err != nil {
		t.Fatal(err)
	}
	encoded := encryptTestAttributes(t, key.AESKey[:], "vector.txt")
	attributes, err := DecryptAttributes(encoded, key.AESKey[:])
	if err != nil {
		t.Fatalf("DecryptAttributes() error = %v", err)
	}
	if attributes["n"] != "vector.txt" {
		t.Fatalf("attributes = %#v", attributes)
	}
}

func encryptTestAttributes(t *testing.T, key []byte, name string) string {
	t.Helper()
	attributes, err := json.Marshal(map[string]string{"n": name})
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("MEGA"), attributes...)
	if remainder := len(payload) % aes.BlockSize; remainder != 0 {
		payload = append(payload, make([]byte, aes.BlockSize-remainder)...)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(payload))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(ciphertext, payload)
	return base64.RawURLEncoding.EncodeToString(ciphertext)
}
