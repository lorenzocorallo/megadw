package mega

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	upstream "github.com/t3rm1n4l/go-mega"
)

// FileKey is the decoded 256-bit MEGA public file key.
//
// Raw contains the eight protocol words. AESKey is the XOR-derived content
// key, Nonce is the eight-byte file nonce, and MetaMAC is the expected
// condensed file MAC.
type FileKey struct {
	Raw     [32]byte
	AESKey  [16]byte
	Nonce   [8]byte
	MetaMAC [8]byte
}

// NodeKey is a decoded public-folder key. Folder keys may be either 128 or
// 256 bits; both forms normalize to a 128-bit AES key.
type NodeKey struct {
	Raw    []byte
	AESKey [16]byte
}

// DecodeFileKey decodes the eight-word key carried by a public file link.
func DecodeFileKey(encoded string) (FileKey, error) {
	decoded, err := decodeURLBase64(encoded)
	if err != nil {
		return FileKey{}, fmt.Errorf("%w: file key is not URL-safe base64: %v", ErrInvalidKey, err)
	}
	return decodeFileKeyBytes(decoded)
}

func decodeFileKeyBytes(decoded []byte) (FileKey, error) {
	if len(decoded) != 32 {
		return FileKey{}, fmt.Errorf("%w: file key must contain 32 bytes, got %d", ErrInvalidKey, len(decoded))
	}

	var key FileKey
	copy(key.Raw[:], decoded)
	for index := 0; index < 4; index++ {
		left := binary.BigEndian.Uint32(decoded[index*4 : index*4+4])
		right := binary.BigEndian.Uint32(decoded[(index+4)*4 : (index+4)*4+4])
		binary.BigEndian.PutUint32(key.AESKey[index*4:index*4+4], left^right)
	}
	copy(key.Nonce[:], decoded[16:24])
	copy(key.MetaMAC[:], decoded[24:32])
	return key, nil
}

// DecodeNodeKey decodes a public folder key or a decrypted folder node key.
func DecodeNodeKey(encoded string) (NodeKey, error) {
	decoded, err := decodeURLBase64(encoded)
	if err != nil {
		return NodeKey{}, fmt.Errorf("%w: node key is not URL-safe base64: %v", ErrInvalidKey, err)
	}
	return decodeNodeKeyBytes(decoded)
}

func decodeNodeKeyBytes(decoded []byte) (NodeKey, error) {
	if len(decoded) != 16 && len(decoded) != 32 {
		return NodeKey{}, fmt.Errorf("%w: node key must contain 16 or 32 bytes, got %d", ErrInvalidKey, len(decoded))
	}

	key := NodeKey{Raw: append([]byte(nil), decoded...)}
	if len(decoded) == 16 {
		copy(key.AESKey[:], decoded)
		return key, nil
	}
	for index := 0; index < 4; index++ {
		left := binary.BigEndian.Uint32(decoded[index*4 : index*4+4])
		right := binary.BigEndian.Uint32(decoded[(index+4)*4 : (index+4)*4+4])
		binary.BigEndian.PutUint32(key.AESKey[index*4:index*4+4], left^right)
	}
	return key, nil
}

// DecryptNodeKey decrypts the AES-ECB-wrapped node key returned in a public
// folder listing. The returned bytes are decoded according to the node type
// by the folder resolver.
func DecryptNodeKey(encoded string, master NodeKey) ([]byte, error) {
	ciphertext, err := decodeURLBase64(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: encrypted node key: %v", ErrInvalidKey, err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: encrypted node key is not AES-block aligned", ErrInvalidKey)
	}
	block, err := aes.NewCipher(master.AESKey[:])
	if err != nil {
		return nil, fmt.Errorf("%w: node key cipher: %v", ErrInvalidKey, err)
	}
	plaintext := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += aes.BlockSize {
		block.Decrypt(plaintext[offset:offset+aes.BlockSize], ciphertext[offset:offset+aes.BlockSize])
	}
	if len(plaintext) != 16 && len(plaintext) != 32 {
		return nil, fmt.Errorf("%w: decrypted node key has %d bytes", ErrInvalidKey, len(plaintext))
	}
	return plaintext, nil
}

// DecryptAttributes decrypts the AES-CBC/zero-IV attribute field returned by
// MEGA metadata commands. The plaintext is the literal MEGA prefix followed
// by a zero-padded JSON object.
func DecryptAttributes(encoded string, aesKey []byte) (map[string]any, error) {
	ciphertext, err := decodeURLBase64(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: attribute base64: %v", ErrInvalidAttributes, err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: attribute ciphertext is not AES-block aligned", ErrInvalidAttributes)
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("%w: attribute cipher: %v", ErrInvalidAttributes, err)
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(plaintext, ciphertext)
	if len(plaintext) < 4 || string(plaintext[:4]) != "MEGA" {
		return nil, fmt.Errorf("%w: missing MEGA prefix", ErrInvalidAttributes)
	}
	payload := strings.TrimRight(string(plaintext[4:]), "\x00")
	attributes := make(map[string]any)
	if err := json.Unmarshal([]byte(payload), &attributes); err != nil {
		return nil, fmt.Errorf("%w: JSON: %v", ErrInvalidAttributes, err)
	}
	return attributes, nil
}

// NewCTR returns a MEGA AES-CTR stream positioned at an arbitrary plaintext
// offset. The offset may be unaligned; the first partial counter block is
// discarded before the caller receives the stream.
func NewCTR(key FileKey, offset int64) (cipher.Stream, error) {
	if offset < 0 {
		return nil, fmt.Errorf("%w: negative AES-CTR offset", ErrInvalidKey)
	}
	block, err := aes.NewCipher(key.AESKey[:])
	if err != nil {
		return nil, fmt.Errorf("%w: content cipher: %v", ErrInvalidKey, err)
	}
	var counter [aes.BlockSize]byte
	copy(counter[:8], key.Nonce[:])
	binary.BigEndian.PutUint64(counter[8:], uint64(offset/aes.BlockSize))
	stream := cipher.NewCTR(block, counter[:])
	if remainder := offset % aes.BlockSize; remainder != 0 {
		skip := make([]byte, remainder)
		stream.XORKeyStream(skip, skip)
	}
	return stream, nil
}

// DecryptAt decrypts a range of MEGA ciphertext at its absolute file offset.
func DecryptAt(ciphertext []byte, key FileKey, offset int64) ([]byte, error) {
	stream, err := NewCTR(key, offset)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	stream.XORKeyStream(plaintext, ciphertext)
	return plaintext, nil
}

// CryptAt applies the same stream transform used for deterministic protocol
// fixtures. AES-CTR encryption and decryption are the same operation.
func CryptAt(plaintext []byte, key FileKey, offset int64) ([]byte, error) {
	return DecryptAt(plaintext, key, offset)
}

func decodeURLBase64(value string) ([]byte, error) {
	// go-mega's public Base64ToBytes helper is intentionally the only upstream
	// primitive reused here. It validates MEGA's unpadded URL-safe alphabet;
	// public-link parsing, metadata, payload URLs, and transfer behavior remain
	// project-owned because go-mega does not implement those APIs.
	return upstream.Base64ToBytes(value)
}
