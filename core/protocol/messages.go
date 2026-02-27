package protocol

// Message is a base protocol envelope for core communication.
type Message struct {
	ID      string
	Version string
	Type    string
}
