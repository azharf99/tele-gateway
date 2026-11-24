// internal/repository/crypto.go
package repository

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

const encryptedPrefix = "enc:v1:"

// SecretBox encrypts and decrypts sensitive account fields (2FA password, custom app hash)
// with AES-256-GCM using a key derived from the configured secret.
type SecretBox struct {
	key []byte
}

func NewSecretBox(secret string) *SecretBox {
	if strings.TrimSpace(secret) == "" {
		return &SecretBox{}
	}
	sum := sha256.Sum256([]byte(secret))
	return &SecretBox{key: sum[:]}
}

func (b *SecretBox) Encrypt(plain string) (string, error) {
	if plain == "" || b == nil || len(b.key) == 0 {
		return plain, nil
	}
	if strings.HasPrefix(plain, encryptedPrefix) {
		return plain, nil
	}
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encryptedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (b *SecretBox) Decrypt(stored string) (string, error) {
	if stored == "" || !strings.HasPrefix(stored, encryptedPrefix) {
		return stored, nil
	}
	if b == nil || len(b.key) == 0 {
		return "", errors.New("encryption key not configured to decrypt stored secret")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encryptedPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
