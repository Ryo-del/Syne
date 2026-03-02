package messaging

import (
	"Syne/core/protocol"
	"Syne/core/transport"
	"fmt"
	"net"
	"sync"
)

// Messenger coordinates outgoing/incoming message flow.
type Messenger struct {
	Peers     map[string]*transport.Peer
	addrIndex map[string]string
	mu        sync.RWMutex
	Inbox     chan protocol.Message
}

func NewMessenger(peers map[string]*transport.Peer, bufSize int) *Messenger {
	addrIndex := make(map[string]string, len(peers))
	for id, peer := range peers {
		if peer == nil || peer.Addr == nil {
			continue
		}
		addrIndex[peer.Addr.String()] = id
	}

	return &Messenger{
		Peers:     peers,
		addrIndex: addrIndex,
		Inbox:     make(chan protocol.Message, bufSize),
	}
}

func (m *Messenger) RegisterPeer(peerID string, peer *transport.Peer) {
	if peer == nil || peer.Addr == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Peers[peerID] = peer
	m.addrIndex[peer.Addr.String()] = peerID
}

func (m *Messenger) ResolvePeerIDByAddr(addr *net.TCPAddr) (string, error) {
	if addr == nil {
		return "", fmt.Errorf("sender address is nil")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.addrIndex[addr.String()]
	if !ok {
		return "", fmt.Errorf("peer not found for address: %s", addr.String())
	}
	return id, nil
}
