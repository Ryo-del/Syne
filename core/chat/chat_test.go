package chat

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempCWD(t *testing.T) string {
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
	return tmp
}

func TestContactCRUD(t *testing.T) {
	_ = withTempCWD(t)

	if err := AddContact(Contact{Name: "alice", PeerID: "A", IP: "127.0.0.1", Port: "3000"}); err != nil {
		t.Fatalf("add contact: %v", err)
	}
	if err := AddContact(Contact{Name: "bob", PeerID: "B", IP: "127.0.0.1", Port: "3001"}); err != nil {
		t.Fatalf("add contact: %v", err)
	}

	list, err := ListContacts()
	if err != nil {
		t.Fatalf("list contacts: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 contacts, got %d", len(list))
	}

	got, err := FindContact("alice")
	if err != nil || got.PeerID != "A" {
		t.Fatalf("find alice failed: %+v, err=%v", got, err)
	}

	if err := RenameContact("A", "Artem"); err != nil {
		t.Fatalf("rename by peer id: %v", err)
	}
	got, err = FindContact("Artem")
	if err != nil || got.PeerID != "A" {
		t.Fatalf("find renamed contact failed: %+v, err=%v", got, err)
	}

	if err := DeleteContact("B"); err != nil {
		t.Fatalf("delete by peer id: %v", err)
	}
	list, err = ListContacts()
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(list) != 1 || list[0].PeerID != "A" {
		t.Fatalf("unexpected contacts after delete: %+v", list)
	}

	if _, err := os.Stat(filepath.Join("data", "contacts", "contacts.jsonl")); err != nil {
		t.Fatalf("contacts file missing: %v", err)
	}
}

func TestRenameConflict(t *testing.T) {
	_ = withTempCWD(t)

	_ = AddContact(Contact{Name: "alice", PeerID: "A", IP: "127.0.0.1", Port: "3000"})
	_ = AddContact(Contact{Name: "bob", PeerID: "B", IP: "127.0.0.1", Port: "3001"})

	if err := RenameContact("bob", "alice"); err == nil {
		t.Fatalf("expected rename conflict error")
	}
}
