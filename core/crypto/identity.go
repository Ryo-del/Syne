package crypto

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

const identityFileName = ".identity"

type Identity struct {
	PeerID          string
	PrivateKey      libcrypto.PrivKey
	PublicKey       libcrypto.PubKey
	PrivateKeyBytes []byte
	PublicKeyBytes  []byte
	Path            string
}

type storedIdentity struct {
	Version    int    `json:"version"`
	PrivateKey []byte `json:"private_key"`
	CreatedAt  int64  `json:"created_at"`
}

func IdentityPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, identityFileName), nil
}

func LoadOrCreateIdentity() (*Identity, error) {
	path, err := IdentityPath()
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err == nil {
		id, err := parseStoredIdentity(raw, path)
		if err != nil {
			return nil, err
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, err
		}
		return id, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	priv, _, err := libcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	privBytes, err := libcrypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	record := storedIdentity{
		Version:    1,
		PrivateKey: privBytes,
		CreatedAt:  time.Now().UnixMilli(),
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return nil, err
	}
	return parseStoredIdentity(payload, path)
}

func parseStoredIdentity(raw []byte, path string) (*Identity, error) {
	var record storedIdentity
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("invalid %s file: %w", identityFileName, err)
	}
	if record.Version != 1 {
		return nil, fmt.Errorf("unsupported %s version: %d", identityFileName, record.Version)
	}
	if len(record.PrivateKey) == 0 {
		return nil, fmt.Errorf("%s private key is empty", identityFileName)
	}

	priv, err := libcrypto.UnmarshalPrivateKey(record.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid %s private key: %w", identityFileName, err)
	}
	pub := priv.GetPublic()
	if pub == nil {
		return nil, fmt.Errorf("%s public key is nil", identityFileName)
	}
	pubBytes, err := libcrypto.MarshalPublicKey(pub)
	if err != nil {
		return nil, err
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(pid.String()) == "" {
		return nil, fmt.Errorf("peer id is empty")
	}
	return &Identity{
		PeerID:          pid.String(),
		PrivateKey:      priv,
		PublicKey:       pub,
		PrivateKeyBytes: append([]byte(nil), record.PrivateKey...),
		PublicKeyBytes:  pubBytes,
		Path:            path,
	}, nil
}
