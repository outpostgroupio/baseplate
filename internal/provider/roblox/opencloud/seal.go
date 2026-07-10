package opencloud

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// seal encrypts plaintext as a LibSodium sealed box (crypto_box_seal) for the
// universe's base64-encoded X25519 public key and returns the base64 ciphertext,
// matching the Open Cloud secrets requirement ("encrypted using LibSodium sealed
// box and encoded in base64"). box.SealAnonymous is pure Go, so the CGO_ENABLED=0
// build constraint holds.
func seal(publicKeyB64 string, plaintext []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return "", fmt.Errorf("opencloud: decoding secret public key: %w", err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("opencloud: secret public key is %d bytes, want 32", len(raw))
	}
	var pk [32]byte
	copy(pk[:], raw)
	sealed, err := box.SealAnonymous(nil, plaintext, &pk, rand.Reader)
	if err != nil {
		return "", fmt.Errorf("opencloud: sealing secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}
