package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type BlockedPeer struct {
	Name    string `json:"name,omitempty"`
	PeerID  string `json:"peer_id"`
	AddedAt int64  `json:"added_at"`
	Reason  string `json:"reason,omitempty"`
}

func IsBlocked(peerID string) (bool, error) {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return false, fmt.Errorf("peer_id is required")
	}
	items, err := ListBlocked()
	if err != nil {
		return false, err
	}
	for _, it := range items {
		if it.PeerID == peerID {
			return true, nil
		}
	}
	return false, nil
}

func AddBlocked(query, reason string) error {
	query = strings.TrimSpace(query)
	reason = strings.TrimSpace(reason)
	if query == "" {
		return fmt.Errorf("query is required")
	}

	peerID := query
	name := ""
	if c, err := FindContact(query); err == nil {
		peerID = c.PeerID
		name = c.Name
	}

	items, err := ListBlocked()
	if err != nil {
		return err
	}

	for i := range items {
		if items[i].PeerID == peerID {
			if name != "" {
				items[i].Name = name
			}
			if reason != "" {
				items[i].Reason = reason
			}
			return writeBlocked(items)
		}
	}

	items = append(items, BlockedPeer{
		Name:    name,
		PeerID:  peerID,
		AddedAt: time.Now().UnixMilli(),
		Reason:  reason,
	})
	return writeBlocked(items)
}

func RemoveBlocked(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return fmt.Errorf("query is required")
	}
	items, err := ListBlocked()
	if err != nil {
		return err
	}
	found := -1
	for i := range items {
		if items[i].PeerID == query || (items[i].Name != "" && strings.EqualFold(items[i].Name, query)) {
			found = i
			break
		}
	}
	if found == -1 {
		return fmt.Errorf("blocked peer not found: %s", query)
	}
	items = append(items[:found], items[found+1:]...)
	return writeBlocked(items)
}

func ListBlocked() ([]BlockedPeer, error) {
	path, err := blocklistFilePath()
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []BlockedPeer{}, nil
		}
		return nil, err
	}
	content := strings.TrimSpace(string(raw))
	if content == "" {
		return []BlockedPeer{}, nil
	}

	// Backward compatibility: allow JSON array as well.
	if strings.HasPrefix(content, "[") {
		var arr []BlockedPeer
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var items []BlockedPeer
	decoder := json.NewDecoder(file)
	for {
		var it BlockedPeer
		if err := decoder.Decode(&it); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if strings.TrimSpace(it.PeerID) == "" {
			continue
		}
		items = append(items, it)
	}
	return items, nil
}

func writeBlocked(items []BlockedPeer) error {
	path, err := blocklistFilePath()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	for _, it := range items {
		if strings.TrimSpace(it.PeerID) == "" {
			continue
		}
		if err := enc.Encode(it); err != nil {
			return err
		}
	}
	return nil
}

func blocklistFilePath() (string, error) {
	dir := filepath.Join("data", "blocklist")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "blocklist.jsonl"), nil
}

