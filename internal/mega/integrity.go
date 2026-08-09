package mega

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	firstMegaChunkSize = 128 * 1024
	steadyMegaChunk    = 1024 * 1024
)

// VerifyIntegrity verifies the MEGA condensed MAC for plaintext file data.
// The reader is consumed exactly size bytes and never buffered in full.
func VerifyIntegrity(reader io.Reader, size int64, key FileKey) error {
	if size < 0 {
		return fmt.Errorf("%w: negative file size", ErrIntegrityMismatch)
	}
	if size == 0 {
		return nil
	}

	block, err := aes.NewCipher(key.AESKey[:])
	if err != nil {
		return fmt.Errorf("%w: content cipher: %v", ErrIntegrityMismatch, err)
	}
	macIV := make([]byte, aes.BlockSize)
	copy(macIV[:8], key.Nonce[:])
	copy(macIV[8:], key.Nonce[:])
	chunkMACs := cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize))

	remaining := size
	chunkNumber := 1
	var finalMAC [aes.BlockSize]byte
	for remaining > 0 {
		chunkSize := megaChunkSize(chunkNumber)
		if int64(chunkSize) > remaining {
			chunkSize = int(remaining)
		}
		plain := make([]byte, chunkSize)
		if _, err := io.ReadFull(reader, plain); err != nil {
			return fmt.Errorf("%w: read integrity data: %v", ErrIntegrityMismatch, err)
		}

		paddedSize := (len(plain) + aes.BlockSize - 1) / aes.BlockSize * aes.BlockSize
		padded := make([]byte, paddedSize)
		copy(padded, plain)
		chunkCipher := cipher.NewCBCEncrypter(block, macIV)
		var chunkMAC [aes.BlockSize]byte
		for offset := 0; offset < len(padded); offset += aes.BlockSize {
			chunkCipher.CryptBlocks(chunkMAC[:], padded[offset:offset+aes.BlockSize])
		}
		chunkMACs.CryptBlocks(finalMAC[:], chunkMAC[:])

		remaining -= int64(chunkSize)
		chunkNumber++
	}

	words := [4]uint32{
		binary.BigEndian.Uint32(finalMAC[0:4]),
		binary.BigEndian.Uint32(finalMAC[4:8]),
		binary.BigEndian.Uint32(finalMAC[8:12]),
		binary.BigEndian.Uint32(finalMAC[12:16]),
	}
	actual := [8]byte{}
	binary.BigEndian.PutUint32(actual[0:4], words[0]^words[1])
	binary.BigEndian.PutUint32(actual[4:8], words[2]^words[3])
	if subtle.ConstantTimeCompare(actual[:], key.MetaMAC[:]) != 1 {
		return ErrIntegrityMismatch
	}
	return nil
}

func megaChunkSize(number int) int {
	if number <= 8 {
		return number * firstMegaChunkSize
	}
	return steadyMegaChunk
}
