package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempDir(t *testing.T) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestLoadOrCreateIdentityPersists0600File(t *testing.T) {
	withTempDir(t)

	id, err := LoadOrCreateIdentity()
	if err != nil {
		t.Fatalf("load or create identity: %v", err)
	}
	if id.PeerID == "" {
		t.Fatalf("peer id should not be empty")
	}

	path := filepath.Join(".", identityFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat identity file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected %s mode 0600, got %o", identityFileName, info.Mode().Perm())
	}
}

func TestLoadOrCreateIdentityRejectsBrokenFile(t *testing.T) {
	withTempDir(t)

	if err := os.WriteFile(identityFileName, []byte(`{"version":1,"private_key":"broken"}`), 0o600); err != nil {
		t.Fatalf("write broken identity: %v", err)
	}
	if _, err := LoadOrCreateIdentity(); err == nil {
		t.Fatalf("expected broken identity file error")
	}
}
