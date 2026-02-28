package messaging

import (
	transform "Syne/core/transport"
	"fmt"
	"net"
)

// Messenger coordinates outgoing/incoming message flow.
type Messenger struct {
	Conn  *net.UDPConn
	Peers map[string]*transform.Peer
	Inbox chan Messege
}
type Messege struct {
	From    string
	Content []byte
}

func NewMessenger(conn *net.UDPConn, peers map[string]*transform.Peer, bufSize int) *Messenger {
	return &Messenger{
		Conn:  conn,
		Peers: peers,
		Inbox: make(chan Messege, bufSize),
	}
}
func (m *Messenger) SendMessage(connID string, data []byte) error {
	peer, ok := m.Peers[connID]
	if !ok {
		return fmt.Errorf("peer not found: %s", connID)
	}
	return transform.SendUDP(m.Conn, peer, data)
}

func (m *Messenger) ReceiveLoop() {

	for {
		data, sender, err := transform.ReceptionUDP(m.Conn)
		if err != nil {
			fmt.Printf("Error receiving message: %v\n", err)
			return
		}
		for id, peer := range m.Peers {
			if peer.Addr == nil {
				continue
			}
			if peer.Addr.String() == sender.String() {
				m.Inbox <- Messege{From: id, Content: data}
				break
			}
		}
	}
}
