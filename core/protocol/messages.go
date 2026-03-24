package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
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

const ProtocolVersion uint8 = 2

const (
	TargetPeer TargetType = iota + 1
	TargetGroup
	TargetBroadcast
)

type Strategy uint8

const (
	StrategyUnknown Strategy = iota
	StrategyDirect
	StrategyRelay
	StrategyHop
	StrategyOffline
)

type Message struct {
	Version   uint8       `json:"version"`
	Type      MessageType `json:"type"`
	Target    TargetType  `json:"target"`
	ID        string      `json:"id"`
	Strategy  Strategy    `json:"strategy"`
	Signature []byte      `json:"signature"`
	TTL       int         `json:"ttl,omitempty"`

	TargetID string `json:"target_id"`
	ChatID   string `json:"chat_id"`
	From     string `json:"from"`

	FromPubKey []byte `json:"from_pub_key"`
	Payload    []byte `json:"payload"`
	Timestamp  int64  `json:"timestamp"`
}

type unsignedMessage struct {
	Version    uint8       `json:"version"`
	Type       MessageType `json:"type"`
	Target     TargetType  `json:"target"`
	ID         string      `json:"id"`
	Strategy   Strategy    `json:"strategy"`
	TTL        int         `json:"ttl,omitempty"`
	TargetID   string      `json:"target_id"`
	ChatID     string      `json:"chat_id"`
	From       string      `json:"from"`
	FromPubKey []byte      `json:"from_pub_key"`
	Payload    []byte      `json:"payload"`
	Timestamp  int64       `json:"timestamp"`
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

func (s Strategy) String() string {
	switch s {
	case StrategyDirect:
		return "direct"
	case StrategyRelay:
		return "relay"
	case StrategyHop:
		return "hop"
	case StrategyOffline:
		return "offline"
	default:
		return "unknown"
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

func NewJoin(chatID, fromID, targetID string, fromPubKey []byte) Message {
	return Message{
		Version:    ProtocolVersion,
		Type:       MsgJoin,
		Target:     TargetPeer,
		Strategy:   StrategyDirect,
		ChatID:     strings.TrimSpace(chatID),
		From:       strings.TrimSpace(fromID),
		TargetID:   strings.TrimSpace(targetID),
		FromPubKey: append([]byte(nil), fromPubKey...),
		Timestamp:  time.Now().UnixMilli(),
	}
}

func NewJoinAck(chatID, fromID, targetID string, fromPubKey []byte) Message {
	return Message{
		Version:    ProtocolVersion,
		Type:       MsgJoinAck,
		Target:     TargetPeer,
		Strategy:   StrategyDirect,
		ChatID:     strings.TrimSpace(chatID),
		From:       strings.TrimSpace(fromID),
		TargetID:   strings.TrimSpace(targetID),
		FromPubKey: append([]byte(nil), fromPubKey...),
		Timestamp:  time.Now().UnixMilli(),
	}
}

func (m *Message) EnsureID() error {
	if m == nil {
		return fmt.Errorf("message is nil")
	}
	if strings.TrimSpace(m.ID) != "" {
		return nil
	}
	payload, err := m.unsignedPayload(false)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	m.ID = hex.EncodeToString(sum[:])
	return nil
}

func (m *Message) Sign(priv libcrypto.PrivKey) error {
	if m == nil {
		return fmt.Errorf("message is nil")
	}
	if priv == nil {
		return fmt.Errorf("private key is required")
	}
	pub := priv.GetPublic()
	if pub == nil {
		return fmt.Errorf("public key is nil")
	}
	rawPub, err := libcrypto.MarshalPublicKey(pub)
	if err != nil {
		return err
	}
	m.FromPubKey = rawPub
	if err := m.EnsureID(); err != nil {
		return err
	}
	payload, err := m.unsignedPayload(true)
	if err != nil {
		return err
	}
	sig, err := priv.Sign(payload)
	if err != nil {
		return err
	}
	m.Signature = sig
	return nil
}

func ValidateMessage(msg Message) error {
	if msg.Version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version: %d", msg.Version)
	}
	if strings.TrimSpace(msg.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(msg.From) == "" {
		return fmt.Errorf("from is required")
	}
	if len(msg.FromPubKey) == 0 {
		return fmt.Errorf("from_pub_key is required")
	}
	if len(msg.Signature) == 0 {
		return fmt.Errorf("signature is required")
	}
	if msg.Target != TargetPeer && msg.Target != TargetGroup && msg.Target != TargetBroadcast {
		return fmt.Errorf("invalid target: %d", msg.Target)
	}
	if msg.Strategy < StrategyUnknown || msg.Strategy > StrategyOffline {
		return fmt.Errorf("invalid strategy: %d", msg.Strategy)
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
	if msg.Type == MsgChat && msg.TTL < 0 {
		return fmt.Errorf("ttl must be >= 0")
	}

	expectedID, err := computeMessageID(msg)
	if err != nil {
		return err
	}
	if msg.ID != expectedID {
		return fmt.Errorf("id mismatch")
	}

	pub, err := libcrypto.UnmarshalPublicKey(msg.FromPubKey)
	if err != nil {
		return fmt.Errorf("invalid from_pub_key: %w", err)
	}
	pid, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return fmt.Errorf("derive peer id: %w", err)
	}
	if msg.From != pid.String() {
		return fmt.Errorf("from does not match public key")
	}

	payload, err := msg.unsignedPayload(true)
	if err != nil {
		return err
	}
	ok, err := pub.Verify(payload, msg.Signature)
	if err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

func computeMessageID(msg Message) (string, error) {
	payload, err := msg.unsignedPayload(false)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (m Message) unsignedPayload(includeID bool) ([]byte, error) {
	out := unsignedMessage{
		Version:    m.Version,
		Type:       m.Type,
		Target:     m.Target,
		Strategy:   m.Strategy,
		TTL:        m.TTL,
		TargetID:   strings.TrimSpace(m.TargetID),
		ChatID:     strings.TrimSpace(m.ChatID),
		From:       strings.TrimSpace(m.From),
		FromPubKey: append([]byte(nil), m.FromPubKey...),
		Payload:    append([]byte(nil), m.Payload...),
		Timestamp:  m.Timestamp,
	}
	if includeID {
		out.ID = strings.TrimSpace(m.ID)
	}
	return json.Marshal(out)
}
