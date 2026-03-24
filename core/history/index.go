package history

import (
	"sort"
	"strings"
	"time"
)

type ChatRecord struct {
	ChatID        string `json:"chat_id"`
	PeerID        string `json:"peer_id"`
	Title         string `json:"title"`
	LastMessage   string `json:"last_message"`
	LastTimestamp int64  `json:"last_timestamp"`
}

func ListChatRecords() ([]ChatRecord, error) {
	database, err := getDB()
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(`
		SELECT chat_id, peer_id, title, last_message, last_timestamp
		FROM chats
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []ChatRecord
	for rows.Next() {
		var record ChatRecord
		if err := rows.Scan(
			&record.ChatID,
			&record.PeerID,
			&record.Title,
			&record.LastMessage,
			&record.LastTimestamp,
		); err != nil {
			return nil, err
		}
		if strings.TrimSpace(record.ChatID) == "" {
			continue
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	if err := ensureDB(); err != nil {
		return err
	}

	record.ChatID = strings.TrimSpace(record.ChatID)
	record.PeerID = strings.TrimSpace(record.PeerID)
	record.Title = strings.TrimSpace(record.Title)
	record.LastMessage = strings.TrimSpace(record.LastMessage)
	if record.ChatID == "" {
		return nil
	}

	database, err := getDB()
	if err != nil {
		return err
	}
	_, err = database.Exec(`
		INSERT INTO chats (chat_id, peer_id, title, last_message, last_timestamp)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET
			peer_id = CASE WHEN excluded.peer_id <> '' THEN excluded.peer_id ELSE chats.peer_id END,
			title = CASE WHEN excluded.title <> '' THEN excluded.title ELSE chats.title END,
			last_message = CASE
				WHEN excluded.last_message <> '' OR excluded.last_timestamp > 0 THEN excluded.last_message
				ELSE chats.last_message
			END,
			last_timestamp = CASE
				WHEN excluded.last_message <> '' OR excluded.last_timestamp > 0 THEN excluded.last_timestamp
				ELSE chats.last_timestamp
			END
	`,
		record.ChatID,
		record.PeerID,
		record.Title,
		record.LastMessage,
		record.LastTimestamp,
	)
	return err
}

func UpsertPeerAlias(peerID, name string) error {
	peerID = strings.TrimSpace(peerID)
	name = strings.TrimSpace(name)
	if peerID == "" || name == "" {
		return nil
	}
	database, err := getDB()
	if err != nil {
		return err
	}
	_, err = database.Exec(`
		INSERT INTO peer_aliases (peer_id, name, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			name = excluded.name,
			updated_at = excluded.updated_at
	`, peerID, name, time.Now().UnixMilli())
	return err
}

func LookupPeerAlias(peerID string) (string, error) {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return "", nil
	}
	database, err := getDB()
	if err != nil {
		return "", err
	}
	var name string
	err = database.QueryRow(`SELECT name FROM peer_aliases WHERE peer_id = ?`, peerID).Scan(&name)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(name), nil
}
