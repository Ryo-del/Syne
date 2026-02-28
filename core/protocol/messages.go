package protocol

type MessageType uint8

const (
	MsgJoin MessageType = iota
	MsgLeave
	MsgChat
	MsgDiscovery
	MsgPing
	MsgPong
)

type TargetType uint8

const (
	TargetPeer TargetType = iota + 1
	TargetGroup
	TargetBroadcast
)

type Message struct {
	Version  uint8
	Type     MessageType
	Target   TargetType
	TargetID string
	ChatID   string
	From     string
	Payload  []byte

	Timestamp int64
}
