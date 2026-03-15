package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	X25519PublicKeySize  = 32
	X25519PrivateKeySize = 32
)

func identityKeyPath() (string, error) {
	dir := filepath.Join("data", "crypto")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "identity.x25519"), nil
}

// LoadOrCreateIdentityKey loads a persistent X25519 private key from disk.
// If missing, generates a new one and stores it with mode 0600.
func LoadOrCreateIdentityKey() (*ecdh.PrivateKey, error) {
	path, err := identityKeyPath()
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) < X25519PrivateKeySize {
			return nil, fmt.Errorf("identity key file is too short: %d bytes", len(raw))
		}
		return ecdh.X25519().NewPrivateKey(raw[:X25519PrivateKeySize])
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, priv.Bytes(), 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

func PublicKeyBytes(priv *ecdh.PrivateKey) ([]byte, error) {
	if priv == nil {
		return nil, fmt.Errorf("private key is nil")
	}
	pub := priv.PublicKey()
	if pub == nil {
		return nil, fmt.Errorf("public key is nil")
	}
	return pub.Bytes(), nil
}

func ParseX25519PublicKey(b []byte) (*ecdh.PublicKey, error) {
	if len(b) != X25519PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: %d", len(b))
	}
	return ecdh.X25519().NewPublicKey(b)
}

// DeriveChatKey computes a symmetric key using X25519 ECDH and HKDF-SHA256.
// The derived key is stable for a given (localPriv, remotePub, chatID) triple.
func DeriveChatKey(localPriv *ecdh.PrivateKey, remotePub *ecdh.PublicKey, chatID string) ([]byte, error) {
	if localPriv == nil || remotePub == nil {
		return nil, fmt.Errorf("keys are required")
	}
	secret, err := localPriv.ECDH(remotePub)
	if err != nil {
		return nil, err
	}

	// Minimal context binding:
	// - salt = SHA256(chatID)
	// - info = "syne-chat-v1"
	salt := sha256.Sum256([]byte(chatID))
	r := hkdfSHA256(secret, salt[:], []byte("syne-chat-v1"))

	key := make([]byte, SharedKeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

