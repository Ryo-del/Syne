package history

import (
	"Syne/core/protocol"
	"os"
	"testing"
	"time"
)

func withTempCWD(t *testing.T) {
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

func TestSaveMessageDedupByMessageID(t *testing.T) {
	withTempCWD(t)

	msg := protocol.Message{
		Version:   protocol.ProtocolVersion,
		Type:      protocol.MsgChat,
		Target:    protocol.TargetPeer,
		MessageID: "msg-1",
		TargetID:  "B",
		ChatID:    "dm:A:B",
		From:      "A",
		Payload:   []byte("hello"),
		Timestamp: time.Now().UnixMilli(),
	}

	if err := SaveMessage(msg); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if err := SaveMessage(msg); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	items, err := LoadMessages("dm:A:B")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after dedup, got %d", len(items))
	}
	if items[0].MessageID != "msg-1" {
		t.Fatalf("unexpected message id: %s", items[0].MessageID)
	}
}
