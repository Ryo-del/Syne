package drop

type StoredMessage struct {
	Version   uint8  `json:"version"`
	Type      uint8  `json:"type"`
	Target    uint8  `json:"target"`
	MessageID string `json:"message_id,omitempty"`
	TargetID  string `json:"target_id"`
	ChatID    string `json:"chat_id"`
	From      string `json:"from"`
	Payload   []byte `json:"payload"`
	Timestamp int64  `json:"timestamp"`
}

func IsMessageForMe(msg StoredMessage, localID string) bool {
	return msg.TargetID == localID
}

func DropNext() {
}
