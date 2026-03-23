package app

import (
	corechat "Syne/core/chat"
	"Syne/core/crypto"
	"Syne/core/discovery"
	"Syne/core/history"
	"Syne/core/protocol"
	"Syne/core/transport"
	"context"
	"crypto/ecdh"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const defaultHopTTL = 4

type Config struct {
	LocalID string `json:"local_id"`
	Port    int    `json:"port"`
}

type Snapshot struct {
	LocalID   string                 `json:"local_id"`
	Port      int                    `json:"port"`
	Contacts  []corechat.Contact     `json:"contacts"`
	Blocked   []corechat.BlockedPeer `json:"blocked"`
	Neighbors []PeerPresence         `json:"neighbors"`
	Chats     []ChatSummary          `json:"chats"`
}

type PeerPresence struct {
	PeerID   string `json:"peer_id"`
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	LastSeen int64  `json:"last_seen"`
	Blocked  bool   `json:"blocked"`
}

type ChatSummary struct {
	ChatID        string `json:"chat_id"`
	PeerID        string `json:"peer_id"`
	Title         string `json:"title"`
	Preview       string `json:"preview"`
	LastTimestamp int64  `json:"last_timestamp"`
	KnownAddr     string `json:"known_addr,omitempty"`
	Online        bool   `json:"online"`
	Blocked       bool   `json:"blocked"`
	UnreadCount   int    `json:"unread_count"`
}

type UIMessage struct {
	MessageID string `json:"message_id,omitempty"`
	ChatID    string `json:"chat_id"`
	TargetID  string `json:"target_id"`
	From      string `json:"from"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
	Direction string `json:"direction"`
}

type Event struct {
	Type      string                `json:"type"`
	Timestamp int64                 `json:"timestamp"`
	Peer      *PeerPresence         `json:"peer,omitempty"`
	Chat      *ChatSummary          `json:"chat,omitempty"`
	Message   *UIMessage            `json:"message,omitempty"`
	Contact   *corechat.Contact     `json:"contact,omitempty"`
	Blocked   *corechat.BlockedPeer `json:"blocked,omitempty"`
	Error     string                `json:"error,omitempty"`
}

type rateState struct {
	tokens float64
	last   time.Time
}

type Service struct {
	cfg Config

	ctx    context.Context
	cancel context.CancelFunc

	listener *net.TCPListener

	identityPriv *ecdh.PrivateKey
	identityPub  []byte

	stateMu          sync.RWMutex
	neighbors        map[string]PeerPresence
	peerPubs         map[string]*ecdh.PublicKey
	seen             map[string]time.Time
	unread           map[string]int
	rateStates       map[string]*rateState
	discoveryStarted bool

	subMu       sync.RWMutex
	subscribers map[chan Event]struct{}

	wg sync.WaitGroup
}

func New(config Config) (*Service, error) {
	profile, err := corechat.GetUserData()
	if err != nil {
		return nil, err
	}

	localID := strings.TrimSpace(config.LocalID)

	if localID == "" {
		localID = strings.TrimSpace(profile.ID)
	}

	if localID != "" {
		if err := corechat.SaveUserProfile(corechat.UserData{
			ID: localID,
		}); err != nil {
			return nil, err
		}
	}

	port := config.Port
	if port <= 0 {
		port = 3000
	}
	for !transport.IsPortFree(port) {
		port++
	}

	identityPriv, err := crypto.LoadOrCreateIdentityKey()
	if err != nil {
		return nil, err
	}
	identityPub, err := crypto.PublicKeyBytes(identityPriv)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		cfg: Config{
			LocalID: localID,
			Port:    port,
		},
		ctx:          ctx,
		cancel:       cancel,
		identityPriv: identityPriv,
		identityPub:  identityPub,
		neighbors:    make(map[string]PeerPresence),
		peerPubs:     make(map[string]*ecdh.PublicKey),
		seen:         make(map[string]time.Time),
		unread:       make(map[string]int),
		rateStates:   make(map[string]*rateState),
		subscribers:  make(map[chan Event]struct{}),
	}, nil
}

func (s *Service) Start() error {
	listener, err := transport.ListenTCP(s.cfg.Port)
	if err != nil {
		return err
	}
	s.listener = listener

	if err := s.startDiscoveryIfNeeded(); err != nil {
		_ = listener.Close()
		return err
	}
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Service) Stop() {
	s.cancel()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.wg.Wait()

	s.subMu.Lock()
	for ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = map[chan Event]struct{}{}
	s.subMu.Unlock()
}

func (s *Service) Snapshot() (Snapshot, error) {
	contacts, err := corechat.ListContacts()
	if err != nil {
		return Snapshot{}, err
	}
	blocked, err := corechat.ListBlocked()
	if err != nil {
		return Snapshot{}, err
	}

	s.stateMu.RLock()
	neighbors := make([]PeerPresence, 0, len(s.neighbors))
	for _, item := range s.neighbors {
		neighbors = append(neighbors, item)
	}
	sort.Slice(neighbors, func(i, j int) bool {
		if neighbors[i].LastSeen == neighbors[j].LastSeen {
			return neighbors[i].PeerID < neighbors[j].PeerID
		}
		return neighbors[i].LastSeen > neighbors[j].LastSeen
	})
	s.stateMu.RUnlock()

	chats, err := s.listChats()
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		LocalID:   s.cfg.LocalID,
		Port:      s.cfg.Port,
		Contacts:  contacts,
		Blocked:   blocked,
		Neighbors: neighbors,
		Chats:     chats,
	}, nil
}

func (s *Service) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 32
	}
	ch := make(chan Event, buffer)

	s.subMu.Lock()
	s.subscribers[ch] = struct{}{}
	s.subMu.Unlock()

	return ch, func() {
		s.subMu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.subMu.Unlock()
	}
}

func (s *Service) ListMessages(chatID string) ([]UIMessage, error) {
	items, err := history.LoadMessages(strings.TrimSpace(chatID))
	if err != nil {
		return nil, err
	}

	messages := make([]UIMessage, 0, len(items))
	for _, item := range items {
		direction := "incoming"
		if item.From == s.cfg.LocalID {
			direction = "outgoing"
		}
		messages = append(messages, UIMessage{
			MessageID: item.MessageID,
			ChatID:    item.ChatID,
			TargetID:  item.TargetID,
			From:      item.From,
			Text:      string(item.Payload),
			Timestamp: item.Timestamp,
			Direction: direction,
		})
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Timestamp == messages[j].Timestamp {
			return messages[i].MessageID < messages[j].MessageID
		}
		return messages[i].Timestamp < messages[j].Timestamp
	})
	return messages, nil
}

func (s *Service) OpenPrivateChat(peerID, peerAddr, name string) (ChatSummary, error) {
	if strings.TrimSpace(s.cfg.LocalID) == "" {
		return ChatSummary{}, fmt.Errorf("peer_id is not configured yet")
	}
	peerID = strings.TrimSpace(peerID)
	peerAddr = strings.TrimSpace(peerAddr)
	name = strings.TrimSpace(name)
	if peerID == "" {
		return ChatSummary{}, fmt.Errorf("peer_id is required")
	}
	if blocked, err := corechat.IsBlocked(peerID); err != nil {
		return ChatSummary{}, err
	} else if blocked {
		return ChatSummary{}, fmt.Errorf("peer is blocked: %s", peerID)
	}

	chatID := privateChatID(s.cfg.LocalID, peerID)
	title := name
	if title == "" {
		title = s.lookupPeerTitle(peerID)
	}

	if err := history.TouchChat(history.ChatRecord{
		ChatID: chatID,
		PeerID: peerID,
		Title:  title,
	}); err != nil {
		return ChatSummary{}, err
	}

	if peerAddr != "" {
		if err := s.sendJoin(peerID, peerAddr, chatID); err != nil {
			s.emit(Event{
				Type:      "error",
				Timestamp: time.Now().UnixMilli(),
				Error:     err.Error(),
			})
		}
	} else if peer := s.resolvePeer(peerID, ""); peer != nil {
		if err := s.sendJoin(peerID, peer.Addr.String(), chatID); err != nil {
			s.emit(Event{
				Type:      "error",
				Timestamp: time.Now().UnixMilli(),
				Error:     err.Error(),
			})
		}
	}

	summary, err := s.chatSummaryByID(chatID)
	if err != nil {
		return ChatSummary{}, err
	}
	return summary, nil
}

func (s *Service) SendMessage(chatID, targetID, text string) (UIMessage, error) {
	if strings.TrimSpace(s.cfg.LocalID) == "" {
		return UIMessage{}, fmt.Errorf("peer_id is not configured yet")
	}
	chatID = strings.TrimSpace(chatID)
	targetID = strings.TrimSpace(targetID)
	text = strings.TrimSpace(text)
	if chatID == "" || targetID == "" || text == "" {
		return UIMessage{}, fmt.Errorf("chat_id, target_id and text are required")
	}
	if blocked, err := corechat.IsBlocked(targetID); err != nil {
		return UIMessage{}, err
	} else if blocked {
		return UIMessage{}, fmt.Errorf("peer is blocked: %s", targetID)
	}

	msg := protocol.Message{
		Version:   protocol.ProtocolVersion,
		Type:      protocol.MsgChat,
		Target:    protocol.TargetPeer,
		MessageID: uuid.NewString(),
		HopTTL:    defaultHopTTL,
		TargetID:  targetID,
		ChatID:    chatID,
		From:      s.cfg.LocalID,
		Payload:   []byte(text),
		Timestamp: time.Now().UnixMilli(),
	}
	if err := history.SaveMessage(msg); err != nil {
		return UIMessage{}, err
	}
	if err := history.TouchChat(history.ChatRecord{
		ChatID:        chatID,
		PeerID:        targetID,
		Title:         s.lookupPeerTitle(targetID),
		LastMessage:   text,
		LastTimestamp: msg.Timestamp,
	}); err != nil {
		return UIMessage{}, err
	}

	sendMsg := msg
	peer := s.resolvePeer(targetID, "")
	if peer != nil {
		s.stateMu.RLock()
		remotePub := s.peerPubs[targetID]
		s.stateMu.RUnlock()
		if remotePub != nil {
			key, err := crypto.DeriveChatKey(s.identityPriv, remotePub, chatID)
			if err != nil {
				return UIMessage{}, err
			}
			ciphertext, nonce, err := crypto.EncryptPayload(key, sendMsg.Payload)
			if err != nil {
				return UIMessage{}, err
			}
			sendMsg.Payload = ciphertext
			sendMsg.Nonce = nonce
		}
		if err := s.sendProtocolMessage(peer, sendMsg); err != nil {
			return UIMessage{}, err
		}
	} else if err := s.forwardToNeighbors(sendMsg, nil); err != nil {
		return UIMessage{}, err
	}

	ui := UIMessage{
		MessageID: msg.MessageID,
		ChatID:    msg.ChatID,
		TargetID:  msg.TargetID,
		From:      msg.From,
		Text:      text,
		Timestamp: msg.Timestamp,
		Direction: "outgoing",
	}
	summary, _ := s.chatSummaryByID(chatID)
	s.emit(Event{
		Type:      "message_sent",
		Timestamp: time.Now().UnixMilli(),
		Message:   &ui,
		Chat:      &summary,
	})
	return ui, nil
}

func (s *Service) MarkChatRead(chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return fmt.Errorf("chat_id is required")
	}
	s.stateMu.Lock()
	delete(s.unread, chatID)
	s.stateMu.Unlock()

	summary, err := s.chatSummaryByID(chatID)
	if err != nil {
		return err
	}
	s.emit(Event{
		Type:      "chat_read",
		Timestamp: time.Now().UnixMilli(),
		Chat:      &summary,
	})
	return nil
}

func (s *Service) AddContact(contact corechat.Contact) (corechat.Contact, error) {
	if err := corechat.AddContact(contact); err != nil {
		return corechat.Contact{}, err
	}
	created, err := corechat.FindContact(contact.PeerID)
	if err != nil {
		return corechat.Contact{}, err
	}
	chatID := privateChatID(s.cfg.LocalID, created.PeerID)
	if err := history.TouchChat(history.ChatRecord{
		ChatID: chatID,
		PeerID: created.PeerID,
		Title:  created.Name,
	}); err != nil {
		return corechat.Contact{}, err
	}

	s.emit(Event{
		Type:      "contact_added",
		Timestamp: time.Now().UnixMilli(),
		Contact:   &created,
	})
	if summary, err := s.chatSummaryByID(chatID); err == nil {
		s.emit(Event{
			Type:      "chat_updated",
			Timestamp: time.Now().UnixMilli(),
			Chat:      &summary,
		})
	}
	return created, nil
}

func (s *Service) RenameContact(query, newName string) (corechat.Contact, error) {
	if err := corechat.RenameContact(query, newName); err != nil {
		return corechat.Contact{}, err
	}
	updated, err := corechat.FindContact(newName)
	if err != nil {
		return corechat.Contact{}, err
	}
	s.emit(Event{
		Type:      "contact_updated",
		Timestamp: time.Now().UnixMilli(),
		Contact:   &updated,
	})
	return updated, nil
}

func (s *Service) DeleteContact(query string) error {
	if err := corechat.DeleteContact(query); err != nil {
		return err
	}
	s.emit(Event{
		Type:      "contact_deleted",
		Timestamp: time.Now().UnixMilli(),
	})
	return nil
}

func (s *Service) BlockPeer(query, reason string) (corechat.BlockedPeer, error) {
	if err := corechat.AddBlocked(query, reason); err != nil {
		return corechat.BlockedPeer{}, err
	}
	items, err := corechat.ListBlocked()
	if err != nil {
		return corechat.BlockedPeer{}, err
	}
	for _, item := range items {
		if item.PeerID == query || strings.EqualFold(item.Name, query) {
			s.emit(Event{
				Type:      "peer_blocked",
				Timestamp: time.Now().UnixMilli(),
				Blocked:   &item,
			})
			return item, nil
		}
	}
	return corechat.BlockedPeer{}, fmt.Errorf("blocked peer not found after update")
}

func (s *Service) UnblockPeer(query string) error {
	if err := corechat.RemoveBlocked(query); err != nil {
		return err
	}
	s.emit(Event{
		Type:      "peer_unblocked",
		Timestamp: time.Now().UnixMilli(),
	})
	return nil
}

func (s *Service) UpdateLocalPeerID(peerID string) error {
	peerID = strings.TrimSpace(peerID)
	if err := validatePeerID(peerID); err != nil {
		return err
	}

	s.stateMu.Lock()
	if strings.TrimSpace(s.cfg.LocalID) != "" {
		s.stateMu.Unlock()
		return fmt.Errorf("peer_id is already set")
	}
	s.cfg.LocalID = peerID
	s.stateMu.Unlock()

	if err := corechat.SaveUserProfile(corechat.UserData{
		ID: peerID,
	}); err != nil {
		s.stateMu.Lock()
		s.cfg.LocalID = ""
		s.stateMu.Unlock()
		return err
	}

	return s.startDiscoveryIfNeeded()
}

func (s *Service) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, sender, err := transport.AcceptTCP(s.listener)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.ctx.Err() != nil {
				return
			}
			s.emit(Event{
				Type:      "error",
				Timestamp: time.Now().UnixMilli(),
				Error:     err.Error(),
			})
			continue
		}

		s.wg.Add(1)
		go func(conn net.Conn, sender *net.TCPAddr) {
			defer s.wg.Done()
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			data, err := transport.ReceiveTCP(conn, 64*1024)
			if err != nil {
				s.emit(Event{
					Type:      "error",
					Timestamp: time.Now().UnixMilli(),
					Error:     fmt.Sprintf("receive error from %s: %v", sender, err),
				})
				return
			}

			msg, err := protocol.UnmarshalMessage(data)
			if err != nil {
				s.emit(Event{
					Type:      "error",
					Timestamp: time.Now().UnixMilli(),
					Error:     fmt.Sprintf("decode error from %s: %v", sender, err),
				})
				return
			}
			if err := protocol.ValidateMessage(msg); err != nil {
				s.emit(Event{
					Type:      "error",
					Timestamp: time.Now().UnixMilli(),
					Error:     fmt.Sprintf("invalid message from %s: %v", sender, err),
				})
				return
			}
			s.handleIncoming(msg, sender)
		}(conn, sender)
	}
}

func (s *Service) handleIncoming(msg protocol.Message, sender *net.TCPAddr) {
	now := time.Now()
	switch msg.Type {
	case protocol.MsgJoin:
		if blocked, err := corechat.IsBlocked(msg.From); err != nil {
			s.emitError(err)
			return
		} else if blocked {
			return
		}
		if !s.allowRate(senderKey(sender), now, 8, 16) {
			return
		}
		s.registerNeighbor(msg.From, sender.String())
		s.rememberPeerPub(msg.From, msg.FromPub)

		ack := protocol.NewJoinAck(msg.ChatID, s.cfg.LocalID, msg.From)
		ack.FromPub = s.identityPub
		if err := s.sendAck(ack, sender); err != nil {
			s.emitError(err)
		}
		if summary, err := s.chatSummaryByID(msg.ChatID); err == nil {
			s.emit(Event{
				Type:      "chat_updated",
				Timestamp: now.UnixMilli(),
				Chat:      &summary,
			})
		}
	case protocol.MsgJoinAck:
		if blocked, err := corechat.IsBlocked(msg.From); err != nil {
			s.emitError(err)
			return
		} else if blocked {
			return
		}
		s.registerNeighbor(msg.From, sender.String())
		s.rememberPeerPub(msg.From, msg.FromPub)
		if summary, err := s.chatSummaryByID(msg.ChatID); err == nil {
			s.emit(Event{
				Type:      "chat_updated",
				Timestamp: now.UnixMilli(),
				Chat:      &summary,
			})
		}

	case protocol.MsgChat:
		if msg.MessageID == "" {
			return
		}
		if blocked, err := corechat.IsBlocked(msg.From); err != nil {
			s.emitError(err)
			return
		} else if blocked {
			return
		}
		if !s.allowRate(senderKey(sender), now, 20, 40) {
			return
		}
		if s.isSeen(msg.MessageID, now, 10*time.Minute) {
			return
		}
		s.registerNeighbor(msg.From, sender.String())

		if msg.TargetID != s.cfg.LocalID {
			if msg.HopTTL <= 1 {
				return
			}
			msg.HopTTL--
			_ = s.forwardToNeighbors(msg, sender)
			return
		}

		payload := msg.Payload
		if len(msg.Nonce) > 0 {
			s.stateMu.RLock()
			remotePub := s.peerPubs[msg.From]
			s.stateMu.RUnlock()
			if remotePub == nil {
				return
			}
			key, err := crypto.DeriveChatKey(s.identityPriv, remotePub, msg.ChatID)
			if err != nil {
				s.emitError(err)
				return
			}
			plain, err := crypto.DecryptPayload(key, msg.Nonce, msg.Payload)
			if err != nil {
				s.emitError(err)
				return
			}
			payload = plain
		}

		plainMsg := msg
		plainMsg.Payload = payload
		plainMsg.Nonce = nil
		if err := history.SaveMessage(plainMsg); err != nil {
			s.emitError(err)
			return
		}
		if err := history.TouchChat(history.ChatRecord{
			ChatID:        plainMsg.ChatID,
			PeerID:        plainMsg.From,
			Title:         s.lookupPeerTitle(plainMsg.From),
			LastMessage:   string(payload),
			LastTimestamp: plainMsg.Timestamp,
		}); err != nil {
			s.emitError(err)
		}

		s.stateMu.Lock()
		s.unread[plainMsg.ChatID]++
		s.stateMu.Unlock()

		ui := UIMessage{
			MessageID: plainMsg.MessageID,
			ChatID:    plainMsg.ChatID,
			TargetID:  plainMsg.TargetID,
			From:      plainMsg.From,
			Text:      string(payload),
			Timestamp: plainMsg.Timestamp,
			Direction: "incoming",
		}
		summary, _ := s.chatSummaryByID(plainMsg.ChatID)
		s.emit(Event{
			Type:      "message_received",
			Timestamp: now.UnixMilli(),
			Message:   &ui,
			Chat:      &summary,
		})
	}
}

func (s *Service) registerNeighbor(peerID, addr string) {
	peerID = strings.TrimSpace(peerID)
	addr = strings.TrimSpace(addr)
	if peerID == "" || peerID == s.cfg.LocalID || addr == "" {
		return
	}
	blocked, _ := corechat.IsBlocked(peerID)

	item := PeerPresence{
		PeerID:   peerID,
		Name:     s.lookupPeerTitle(peerID),
		Addr:     addr,
		LastSeen: time.Now().UnixMilli(),
		Blocked:  blocked,
	}

	s.stateMu.Lock()
	s.neighbors[peerID] = item
	s.stateMu.Unlock()

	s.emit(Event{
		Type:      "peer_discovered",
		Timestamp: item.LastSeen,
		Peer:      &item,
	})
}

func (s *Service) lookupPeerName(peerID string) string {
	contacts, err := corechat.ListContacts()
	if err != nil {
		return ""
	}
	for _, item := range contacts {
		if item.PeerID == peerID {
			return item.Name
		}
	}
	return ""
}

func (s *Service) lookupPeerTitle(peerID string) string {
	if name := s.lookupPeerName(peerID); name != "" {
		return name
	}
	return peerID
}

func (s *Service) listChats() ([]ChatSummary, error) {
	records, err := history.ListChatRecords()
	if err != nil {
		return nil, err
	}
	contacts, err := corechat.ListContacts()
	if err != nil {
		return nil, err
	}
	blocked, err := corechat.ListBlocked()
	if err != nil {
		return nil, err
	}

	contactMap := make(map[string]corechat.Contact, len(contacts))
	for _, item := range contacts {
		contactMap[item.PeerID] = item
	}
	blockedSet := make(map[string]struct{}, len(blocked))
	for _, item := range blocked {
		blockedSet[item.PeerID] = struct{}{}
	}
	chats := make([]ChatSummary, 0, len(records))
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for _, record := range records {
		addr := ""
		if item, ok := contactMap[record.PeerID]; ok {
			addr = item.Address()
		} else if neighbor, ok := s.neighbors[record.PeerID]; ok {
			addr = neighbor.Addr
		}
		title := strings.TrimSpace(record.Title)
		if item, ok := contactMap[record.PeerID]; ok && item.Name != "" {
			title = item.Name
		}
		if title == "" {
			title = record.PeerID
		}
		_, isBlocked := blockedSet[record.PeerID]
		_, online := s.neighbors[record.PeerID]

		chats = append(chats, ChatSummary{
			ChatID:        record.ChatID,
			PeerID:        record.PeerID,
			Title:         title,
			Preview:       record.LastMessage,
			LastTimestamp: record.LastTimestamp,
			KnownAddr:     addr,
			Online:        online,
			Blocked:       isBlocked,
			UnreadCount:   s.unread[record.ChatID],
		})
	}
	sort.Slice(chats, func(i, j int) bool {
		if chats[i].LastTimestamp == chats[j].LastTimestamp {
			return chats[i].Title < chats[j].Title
		}
		return chats[i].LastTimestamp > chats[j].LastTimestamp
	})
	return chats, nil
}

func (s *Service) chatSummaryByID(chatID string) (ChatSummary, error) {
	chats, err := s.listChats()
	if err != nil {
		return ChatSummary{}, err
	}
	for _, item := range chats {
		if item.ChatID == chatID {
			return item, nil
		}
	}
	return ChatSummary{}, fmt.Errorf("chat not found: %s", chatID)
}

func (s *Service) emit(event Event) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Service) emitError(err error) {
	if err == nil {
		return
	}
	s.emit(Event{
		Type:      "error",
		Timestamp: time.Now().UnixMilli(),
		Error:     err.Error(),
	})
}

func (s *Service) startDiscoveryIfNeeded() error {
	s.stateMu.Lock()
	if s.discoveryStarted || strings.TrimSpace(s.cfg.LocalID) == "" {
		s.stateMu.Unlock()
		return nil
	}
	localID := s.cfg.LocalID
	port := s.cfg.Port
	s.discoveryStarted = true
	s.stateMu.Unlock()

	if err := discovery.StartLANDiscovery(
		s.ctx,
		localID,
		port,
		s.registerNeighbor,
	); err != nil {
		s.stateMu.Lock()
		s.discoveryStarted = false
		s.stateMu.Unlock()
		return err
	}
	return nil
}

func (s *Service) rememberPeerPub(peerID string, raw []byte) {
	if len(raw) != crypto.X25519PublicKeySize {
		return
	}
	pub, err := crypto.ParseX25519PublicKey(raw)
	if err != nil {
		return
	}
	s.stateMu.Lock()
	s.peerPubs[peerID] = pub
	s.stateMu.Unlock()
}

func (s *Service) allowRate(key string, now time.Time, ratePerSec, burst float64) bool {
	if key == "" {
		key = "unknown"
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	state, ok := s.rateStates[key]
	if !ok {
		s.rateStates[key] = &rateState{tokens: burst - 1, last: now}
		return true
	}
	elapsed := now.Sub(state.last).Seconds()
	state.tokens += elapsed * ratePerSec
	if state.tokens > burst {
		state.tokens = burst
	}
	state.last = now
	if state.tokens < 1 {
		return false
	}
	state.tokens--
	return true
}

func (s *Service) isSeen(id string, now time.Time, ttl time.Duration) bool {
	if id == "" {
		return false
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if ts, ok := s.seen[id]; ok && now.Sub(ts) <= ttl {
		return true
	}
	s.seen[id] = now
	for key, ts := range s.seen {
		if now.Sub(ts) > ttl {
			delete(s.seen, key)
		}
	}
	return false
}

func (s *Service) resolvePeer(peerID, addrHint string) *transport.Peer {
	addrHint = strings.TrimSpace(addrHint)
	if addrHint != "" {
		addr, err := net.ResolveTCPAddr("tcp", addrHint)
		if err == nil {
			return &transport.Peer{PeerID: peerID, Addr: addr}
		}
	}

	if contact, err := corechat.FindContact(peerID); err == nil {
		if addr, err := net.ResolveTCPAddr("tcp", contact.Address()); err == nil {
			return &transport.Peer{PeerID: peerID, Addr: addr}
		}
	}

	s.stateMu.RLock()
	neighbor, ok := s.neighbors[peerID]
	s.stateMu.RUnlock()
	if ok {
		if addr, err := net.ResolveTCPAddr("tcp", neighbor.Addr); err == nil {
			return &transport.Peer{PeerID: peerID, Addr: addr}
		}
	}
	return nil
}

func (s *Service) sendJoin(peerID, peerAddr, chatID string) error {
	peer := s.resolvePeer(peerID, peerAddr)
	if peer == nil {
		return fmt.Errorf("peer address not found: %s", peerID)
	}
	join := protocol.NewJoin(chatID, s.cfg.LocalID, peerID)
	join.FromPub = s.identityPub
	return s.sendProtocolMessage(peer, join)
}

func (s *Service) sendAck(msg protocol.Message, sender *net.TCPAddr) error {
	peer := s.resolvePeer(msg.TargetID, "")
	if peer == nil && sender != nil {
		peer = &transport.Peer{PeerID: msg.TargetID, Addr: sender}
	}
	if peer == nil {
		return fmt.Errorf("ack peer not found: %s", msg.TargetID)
	}
	return s.sendProtocolMessage(peer, msg)
}

func (s *Service) sendProtocolMessage(peer *transport.Peer, msg protocol.Message) error {
	if err := protocol.ValidateMessage(msg); err != nil {
		return err
	}
	wire, err := protocol.MarshalMessage(msg)
	if err != nil {
		return err
	}
	return transport.SendTCP(peer, wire)
}

func (s *Service) forwardToNeighbors(msg protocol.Message, sender *net.TCPAddr) error {
	contacts, err := corechat.ListContacts()
	if err != nil {
		return err
	}

	targets := make(map[string]*transport.Peer)
	add := func(peerID, addrStr string) {
		if peerID == "" || peerID == msg.From || addrStr == "" {
			return
		}
		if sender != nil && sender.String() == addrStr {
			return
		}
		if _, ok := targets[addrStr]; ok {
			return
		}
		addr, err := net.ResolveTCPAddr("tcp", addrStr)
		if err != nil {
			return
		}
		targets[addrStr] = &transport.Peer{PeerID: peerID, Addr: addr}
	}

	for _, item := range contacts {
		add(item.PeerID, item.Address())
	}

	s.stateMu.RLock()
	for _, item := range s.neighbors {
		add(item.PeerID, item.Addr)
	}
	s.stateMu.RUnlock()

	if len(targets) == 0 {
		return fmt.Errorf("no neighbors or contacts to forward")
	}
	for _, peer := range targets {
		go func(peer *transport.Peer) {
			_ = s.sendProtocolMessage(peer, msg)
		}(peer)
	}
	return nil
}

func privateChatID(a, b string) string {
	if a <= b {
		return "dm:" + a + ":" + b
	}
	return "dm:" + b + ":" + a
}

func validatePeerID(peerID string) error {
	if peerID == "" {
		return fmt.Errorf("peer_id is required")
	}
	if len(peerID) < 3 || len(peerID) > 32 {
		return fmt.Errorf("peer_id must be 3-32 characters long")
	}
	for _, ch := range peerID {
		isLetter := ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
		isDigit := ch >= '0' && ch <= '9'
		switch {
		case isLetter, isDigit, ch == '-', ch == '_', ch == '.':
			continue
		default:
			return fmt.Errorf("peer_id may contain only letters, numbers, dot, dash and underscore")
		}
	}
	return nil
}

func senderKey(sender *net.TCPAddr) string {
	if sender == nil {
		return "unknown"
	}
	return sender.IP.String()
}
