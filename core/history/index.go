package history

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ChatRecord struct {
	ChatID        string `json:"chat_id"`
	PeerID        string `json:"peer_id"`
	Title         string `json:"title"`
	LastMessage   string `json:"last_message"`
	LastTimestamp int64  `json:"last_timestamp"`
}

func ListChatRecords() ([]ChatRecord, error) {
	path, err := chatIndexFilePath()
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []ChatRecord{}, nil
		}
		return nil, err
	}
	content := strings.TrimSpace(string(raw))
	if content == "" {
		return []ChatRecord{}, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var records []ChatRecord
	decoder := json.NewDecoder(file)
	for {
		var record ChatRecord
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if strings.TrimSpace(record.ChatID) == "" {
			continue
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].LastTimestamp == records[j].LastTimestamp {
			return records[i].ChatID < records[j].ChatID
		}
		return records[i].LastTimestamp > records[j].LastTimestamp
	})
	return records, nil
}

func TouchChat(record ChatRecord) error {
	record.ChatID = strings.TrimSpace(record.ChatID)
	record.PeerID = strings.TrimSpace(record.PeerID)
	record.Title = strings.TrimSpace(record.Title)
	record.LastMessage = strings.TrimSpace(record.LastMessage)
	if record.ChatID == "" {
		return nil
	}

	records, err := ListChatRecords()
	if err != nil {
		return err
	}

	updated := false
	for i := range records {
		if records[i].ChatID != record.ChatID {
			continue
		}
		if record.PeerID != "" {
			records[i].PeerID = record.PeerID
		}
		if record.Title != "" {
			records[i].Title = record.Title
		}
		if record.LastMessage != "" || record.LastTimestamp > 0 {
			records[i].LastMessage = record.LastMessage
			records[i].LastTimestamp = record.LastTimestamp
		}
		updated = true
		break
	}
	if !updated {
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].LastTimestamp == records[j].LastTimestamp {
			return records[i].ChatID < records[j].ChatID
		}
		return records[i].LastTimestamp > records[j].LastTimestamp
	})

	path, err := chatIndexFilePath()
	if err != nil {
		return err
	}

	unlock, err := acquireFileLock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, item := range records {
		if strings.TrimSpace(item.ChatID) == "" {
			continue
		}
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

func chatIndexFilePath() (string, error) {
	historyDir := filepath.Join("data", "history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(historyDir, "chats.jsonl"), nil
}
