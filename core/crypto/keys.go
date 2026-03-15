package crypto

import (
	"crypto/rand"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/chacha20poly1305"
)

const SharedKeySize = chacha20poly1305.KeySize

type KeyPair struct {
	PublicKey  []byte
	PrivateKey []byte
}

func GenerateKeyPair() (KeyPair, error) {
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return KeyPair{}, err
	}
	return KeyPair{
		PublicKey:  key,
		PrivateKey: key,
	}, nil
}

// EncryptPayload шифрует plaintext ключом key (32 байта). Возвращает ciphertext и nonce.
func EncryptPayload(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, chacha20poly1305.NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = aead.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// DecryptPayload расшифровывает ciphertext ключом key с заданным nonce.
func DecryptPayload(key, nonce, ciphertext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

// SharedKeyPath возвращает путь к файлу общего ключа (data/secret.key).
func SharedKeyPath() (string, error) {
	dir := filepath.Join("data", "crypto")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "secret.key"), nil
}

// LoadOrCreateSharedKey читает ключ из data/crypto/secret.key (32 байта).
// Если файла нет — создаёт его с случайным ключом. Один и тот же ключ нужно скопировать
// собеседнику, иначе расшифровка не получится.
func LoadOrCreateSharedKey() ([]byte, error) {
	path, err := SharedKeyPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) >= SharedKeySize {
			return data[:SharedKeySize], nil
		}
		// файл есть, но короткий — перезапишем
	}
	key := make([]byte, SharedKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
