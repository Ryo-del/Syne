package history

import (
	"encoding/json"
	"io"
	"os"
	"time"
)

type StoredMessage struct {
	Version   uint8  `json:"version"`
	Type      uint8  `json:"type"`
	Target    uint8  `json:"target"`
	TargetID  string `json:"target_id"`
	ChatID    string `json:"chat_id"`
	From      string `json:"from"`
	Payload   []byte `json:"payload"`
	Timestamp int64  `json:"timestamp"`
}

func SaveMessage(chatID string, msg []byte, version uint8, msgType uint8, target uint8, targetID string, from string) error {
	file, err := os.OpenFile(chatID+".json", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	stored := StoredMessage{
		Version:   version,
		Type:      msgType,
		Target:    target,
		TargetID:  targetID,
		ChatID:    chatID,
		From:      from,
		Payload:   msg,
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}

	if _, err := file.Write(append(data, []byte("\n")...)); err != nil {
		return err
	}
	return nil
}

func LoadMessages(chatID string) ([]StoredMessage, error) {
	file, err := os.Open(chatID + ".json")
	if err != nil {
		if os.IsNotExist(err) {
			return []StoredMessage{}, nil
		}
		return nil, err
	}
	defer file.Close()

	var messages []StoredMessage
	decoder := json.NewDecoder(file)

	for {
		var stored StoredMessage
		if err := decoder.Decode(&stored); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		messages = append(messages, stored)
	}

	return messages, nil
}
