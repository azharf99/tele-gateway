// internal/repository/crypto_test.go
package repository

import (
	"strings"
	"testing"
)

func TestSecretBox_RoundTrip(t *testing.T) {
	box := NewSecretBox("super-secret-jwt-key-123")

	enc, err := box.Encrypt("my-2fa-password")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(enc, encryptedPrefix) {
		t.Fatalf("expected prefix %q, got %q", encryptedPrefix, enc)
	}
	if enc == "my-2fa-password" {
		t.Fatal("ciphertext must not equal plaintext")
	}

	plain, err := box.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plain != "my-2fa-password" {
		t.Fatalf("Decrypt = %q, want my-2fa-password", plain)
	}

	// Plaintext legacy values pass through untouched
	legacy, err := box.Decrypt("legacy-plaintext")
	if err != nil || legacy != "legacy-plaintext" {
		t.Fatalf("legacy decrypt = (%q, %v)", legacy, err)
	}
}
