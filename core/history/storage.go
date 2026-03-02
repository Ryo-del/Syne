package history

import (
	"Syne/core/protocol"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

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

func SaveMessage(msg protocol.Message) error {
	path, err := historyFilePath(msg.ChatID)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	unlock, err := acquireFileLock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	stored := StoredMessage{
		Version:   msg.Version,
		Type:      uint8(msg.Type),
		Target:    uint8(msg.Target),
		MessageID: msg.MessageID,
		TargetID:  msg.TargetID,
		ChatID:    msg.ChatID,
		From:      msg.From,
		Payload:   msg.Payload,
		Timestamp: msg.Timestamp,
	}

	if stored.MessageID != "" {
		exists, err := hasMessageIDInFile(file, stored.MessageID)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
	}

	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	_, err = file.Write(append(data, '\n'))
	return err
}

func LoadMessages(chatID string) ([]StoredMessage, error) {
	path, err := historyFilePath(chatID)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
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

func historyFilePath(chatID string) (string, error) {
	historyDir := filepath.Join("data", "history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(chatID))
	safeName := hex.EncodeToString(sum[:])
	return filepath.Join(historyDir, safeName+".jsonl"), nil
}

func hasMessageIDInFile(file *os.File, messageID string) (bool, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	decoder := json.NewDecoder(file)
	for {
		var stored StoredMessage
		if err := decoder.Decode(&stored); err != nil {
			if err == io.EOF {
				return false, nil
			}
			return false, err
		}
		if stored.MessageID != "" && stored.MessageID == messageID {
			return true, nil
		}
	}
}

func acquireFileLock(lockPath string) (func(), error) {
	const (
		waitStep = 20 * time.Millisecond
		timeout  = 3 * time.Second
	)

	deadline := time.Now().Add(timeout)
	for {
		lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return func() {
				_ = lockFile.Close()
				_ = os.Remove(lockPath)
			}, nil
		}

		if !os.IsExist(err) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout acquiring history lock: %s", lockPath)
		}
		time.Sleep(waitStep)
	}
}
