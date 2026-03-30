package history

import (
	"Syne/core/protocol"
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

type StoredMessage struct {
	Version   uint8             `json:"version"`
	Type      uint8             `json:"type"`
	Target    uint8             `json:"target"`
	MessageID string            `json:"message_id,omitempty"`
	Strategy  protocol.Strategy `json:"strategy"`
	TargetID  string            `json:"target_id"`
	ChatID    string            `json:"chat_id"`
	From      string            `json:"from"`
	Payload   []byte            `json:"payload"`
	Timestamp int64             `json:"timestamp"`
}

type OutboxItem struct {
	MessageID     string
	ChatID        string
	TargetID      string
	Payload       []byte
	TTL           int
	CreatedAt     int64
	NextAttemptAt int64
	LastError     string
}

var (
	dbMu      sync.Mutex
	db        *sql.DB
	currentDB string
)

func SaveMessage(msg protocol.Message) error {
	database, err := getDB()
	if err != nil {
		return err
	}
	_, err = database.Exec(`
		INSERT OR IGNORE INTO messages (
			message_id, chat_id, target_id, sender_id, payload, timestamp, version, type, target, strategy
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		msg.ID,
		msg.ChatID,
		msg.TargetID,
		msg.From,
		msg.Payload,
		msg.Timestamp,
		msg.Version,
		uint8(msg.Type),
		uint8(msg.Target),
		uint8(msg.Strategy),
	)
	return err
}

func LoadMessages(chatID string) ([]StoredMessage, error) {
	database, err := getDB()
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(`
		SELECT version, type, target, message_id, strategy, target_id, chat_id, sender_id, payload, timestamp
		FROM messages
		WHERE chat_id = ?
		ORDER BY timestamp ASC, message_id ASC
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []StoredMessage
	for rows.Next() {
		var item StoredMessage
		var strategy uint8
		if err := rows.Scan(
			&item.Version,
			&item.Type,
			&item.Target,
			&item.MessageID,
			&strategy,
			&item.TargetID,
			&item.ChatID,
			&item.From,
			&item.Payload,
			&item.Timestamp,
		); err != nil {
			return nil, err
		}
		item.Strategy = protocol.Strategy(strategy)
		messages = append(messages, item)
	}
	return messages, rows.Err()
}

func QueueMessage(msg protocol.Message, nextAttemptAt int64) error {
	database, err := getDB()
	if err != nil {
		return err
	}
	_, err = database.Exec(`
		INSERT INTO outbox (message_id, chat_id, target_id, payload, ttl, created_at, next_attempt_at, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, '')
		ON CONFLICT(message_id) DO UPDATE SET next_attempt_at = excluded.next_attempt_at
	`,
		msg.ID,
		msg.ChatID,
		msg.TargetID,
		msg.Payload,
		msg.TTL,
		msg.Timestamp,
		nextAttemptAt,
	)
	return err
}

func LoadDueOutbox(nowUnixMilli int64, limit int) ([]OutboxItem, error) {
	if limit <= 0 {
		limit = 16
	}
	database, err := getDB()
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(`
		SELECT message_id, chat_id, target_id, payload, ttl, created_at, next_attempt_at, last_error
		FROM outbox
		WHERE next_attempt_at <= ?
		ORDER BY next_attempt_at ASC
		LIMIT ?
	`, nowUnixMilli, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []OutboxItem
	for rows.Next() {
		var item OutboxItem
		if err := rows.Scan(
			&item.MessageID,
			&item.ChatID,
			&item.TargetID,
			&item.Payload,
			&item.TTL,
			&item.CreatedAt,
			&item.NextAttemptAt,
			&item.LastError,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func UpdateOutboxFailure(messageID, errText string, nextAttemptAt int64) error {
	database, err := getDB()
	if err != nil {
		return err
	}
	_, err = database.Exec(`
		UPDATE outbox
		SET last_error = ?, next_attempt_at = ?
		WHERE message_id = ?
	`, errText, nextAttemptAt, messageID)
	return err
}

func DeleteOutbox(messageID string) error {
	database, err := getDB()
	if err != nil {
		return err
	}
	_, err = database.Exec(`DELETE FROM outbox WHERE message_id = ?`, messageID)

	return err
}

func DeletePeerAlias(peerID string) error {
	database, err := getDB()
	if err != nil {
		return err
	}
	_, err = database.Exec(`DELETE FROM peer_aliases WHERE peer_id = ?`, peerID)
	return err
}
func ensureDB() error {
	_, err := getDB()
	return err
}

func getDB() (*sql.DB, error) {
	path, err := sqlitePath()
	if err != nil {
		return nil, err
	}

	dbMu.Lock()
	defer dbMu.Unlock()

	if db != nil && currentDB == path {
		return db, nil
	}
	if db != nil {
		_ = db.Close()
		db = nil
		currentDB = ""
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := database.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 5000;
		CREATE TABLE IF NOT EXISTS messages (
			message_id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			payload BLOB NOT NULL,
			timestamp INTEGER NOT NULL,
			version INTEGER NOT NULL,
			type INTEGER NOT NULL,
			target INTEGER NOT NULL,
			strategy INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages(chat_id, timestamp, message_id);
		CREATE TABLE IF NOT EXISTS chats (
			chat_id TEXT PRIMARY KEY,
			peer_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			last_message TEXT NOT NULL DEFAULT '',
			last_timestamp INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS peer_aliases (
			peer_id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS outbox (
			message_id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			payload BLOB NOT NULL,
			ttl INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			next_attempt_at INTEGER NOT NULL,
			last_error TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		_ = database.Close()
		return nil, err
	}

	db = database
	currentDB = path
	return db, nil
}

func sqlitePath() (string, error) {
	dir := filepath.Join("data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "syne.db"), nil
}

func resetForTests() {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		_ = db.Close()
	}
	db = nil
	currentDB = ""
}
