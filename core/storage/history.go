package storage

// MessageRecord is a storage model for chat history entries.
type MessageRecord struct {
	ChatID    string
	MessageID string
	Body      string
}
