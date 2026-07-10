package opencloud

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func TestSealRoundTrip(t *testing.T) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub[:])
	msg := []byte("super-secret-token")

	cipherB64, err := seal(pubB64, msg)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		t.Fatalf("ciphertext not valid base64: %v", err)
	}
	got, ok := box.OpenAnonymous(nil, cipher, pub, priv)
	if !ok {
		t.Fatal("OpenAnonymous failed to decrypt the sealed box")
	}
	if string(got) != string(msg) {
		t.Fatalf("decrypted %q, want %q", got, msg)
	}
}

func TestSealRejectsBadKey(t *testing.T) {
	if _, err := seal("!!!not-base64!!!", []byte("x")); err == nil {
		t.Fatal("want error on invalid base64 public key")
	}
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, err := seal(short, []byte("x")); err == nil {
		t.Fatal("want error on wrong-length public key")
	}
}
