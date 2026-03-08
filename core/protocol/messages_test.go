package protocol

import (
	"testing"
	"time"
)

func TestMarshalUnmarshalAndValidate(t *testing.T) {
	in := Message{
		Version:   ProtocolVersion,
		Type:      MsgChat,
		Target:    TargetPeer,
		MessageID: "m1",
		TargetID:  "B",
		ChatID:    "dm:A:B",
		From:      "A",
		Payload:   []byte("ping"),
		Timestamp: time.Now().UnixMilli(),
	}

	wire, err := MarshalMessage(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalMessage(wire)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := ValidateMessage(out); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if string(out.Payload) != "ping" || out.From != "A" || out.ChatID != "dm:A:B" {
		t.Fatalf("unexpected decoded message: %+v", out)
	}
}

func TestValidateMessageRejectsInvalid(t *testing.T) {
	msg := Message{
		Version:   ProtocolVersion,
		Type:      MsgChat,
		Target:    0,
		TargetID:  "",
		ChatID:    "",
		From:      "",
		Timestamp: 0,
	}
	if err := ValidateMessage(msg); err == nil {
		t.Fatalf("expected validation error")
	}
}
