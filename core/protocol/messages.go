package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type MessageType uint8

const (
	MsgJoin MessageType = iota
	MsgJoinAck
	MsgLeave
	MsgChat
	MsgDiscovery
)

type TargetType uint8

const ProtocolVersion uint8 = 1

const (
	TargetPeer TargetType = iota + 1
	TargetGroup
	TargetBroadcast
)

type Message struct {
	Version uint8       `json:"version"`
	Type    MessageType `json:"type"`
	Target  TargetType  `json:"target"`

	MessageID string `json:"message_id,omitempty"`
	HopTTL   int    `json:"ttl,omitempty"`
	TargetID string `json:"target_id"`
	ChatID   string `json:"chat_id"`
	From     string `json:"from"`
	FromPub  []byte `json:"from_pub,omitempty"` // X25519 public key (32 bytes) for key agreement/handshake
	Payload  []byte `json:"payload"`
	Nonce    []byte `json:"nonce,omitempty"` // для MsgChat: nonce шифрования (если есть — Payload зашифрован)

	Timestamp int64 `json:"timestamp"`
}

func (t MessageType) String() string {
	switch t {
	case MsgJoin:
		return "join"
	case MsgJoinAck:
		return "join_ack"
	case MsgLeave:
		return "leave"
	case MsgChat:
		return "chat"
	case MsgDiscovery:
		return "discovery"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(t))
	}
}

func MarshalMessage(msg Message) ([]byte, error) {
	return json.Marshal(msg)
}

func UnmarshalMessage(data []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}
func NewJoin(chatID, fromID, targetID string) Message {
	return Message{
		Version:   ProtocolVersion,
		Type:      MsgJoin,
		Target:    TargetPeer,
		ChatID:    chatID,
		From:      fromID,
		TargetID:  targetID,
		Timestamp: time.Now().UnixMilli(),
	}
}
func NewJoinAck(chatID, fromID, targetID string) Message {
	return Message{
		Version:   ProtocolVersion,
		Type:      MsgJoinAck,
		Target:    TargetPeer,
		ChatID:    chatID,
		From:      fromID,
		TargetID:  targetID,
		Timestamp: time.Now().UnixMilli(),
	}
}
func ValidateMessage(msg Message) error {
	if msg.Version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version: %d", msg.Version)
	}
	if msg.From == "" {
		return fmt.Errorf("from is required")
	}
	if msg.Target != TargetPeer && msg.Target != TargetGroup && msg.Target != TargetBroadcast {
		return fmt.Errorf("invalid target: %d", msg.Target)
	}
	if msg.Target != TargetBroadcast && strings.TrimSpace(msg.TargetID) == "" {
		return fmt.Errorf("target_id is required")
	}
	if strings.TrimSpace(msg.ChatID) == "" {
		return fmt.Errorf("chat_id is required")
	}
	if msg.Timestamp <= 0 {
		return fmt.Errorf("timestamp must be > 0")
	}
	if msg.Type == MsgChat && msg.HopTTL < 0 {
		return fmt.Errorf("ttl must be >= 0")
	}
	return nil
}
