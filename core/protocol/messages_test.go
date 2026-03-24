package protocol

import (
	"crypto/rand"
	"testing"
	"time"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestMarshalUnmarshalAndValidate(t *testing.T) {
	priv, _, err := libcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("peer id: %v", err)
	}

	msg := Message{
		Version:   ProtocolVersion,
		Type:      MsgChat,
		Target:    TargetPeer,
		Strategy:  StrategyDirect,
		TTL:       4,
		TargetID:  "B",
		ChatID:    "dm:A:B",
		From:      pid.String(),
		Payload:   []byte("ping"),
		Timestamp: time.Now().UnixMilli(),
	}
	if err := msg.Sign(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}

	wire, err := MarshalMessage(msg)
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
	if string(out.Payload) != "ping" || out.From != pid.String() || out.ChatID != "dm:A:B" {
		t.Fatalf("unexpected decoded message: %+v", out)
	}
	if out.ID == "" {
		t.Fatalf("message id should be generated")
	}
}

func TestValidateMessageRejectsTampering(t *testing.T) {
	priv, _, err := libcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("peer id: %v", err)
	}

	msg := Message{
		Version:   ProtocolVersion,
		Type:      MsgChat,
		Target:    TargetPeer,
		Strategy:  StrategyHop,
		TTL:       3,
		TargetID:  "peer-b",
		ChatID:    "dm:test",
		From:      pid.String(),
		Payload:   []byte("hello"),
		Timestamp: time.Now().UnixMilli(),
	}
	if err := msg.Sign(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}

	msg.Payload = []byte("evil")
	if err := ValidateMessage(msg); err == nil {
		t.Fatalf("expected tamper validation error")
	}
}
