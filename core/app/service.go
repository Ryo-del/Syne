package app

import (
	corechat "Syne/core/chat"
	corecrypto "Syne/core/crypto"
	"Syne/core/history"
	"Syne/core/protocol"
	p2ptransport "Syne/core/transport/p2p"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	defaultHopTTL        = 4
	dedupCapacity        = 1000
	defaultInviteTTL     = 15 * time.Minute
	relayFallbackTimeout = 2 * time.Second
)

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
	Strategy  string `json:"strategy"`
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

type InviteCode struct {
	Code      string `json:"code"`
	PeerID    string `json:"peer_id"`
	ExpiresAt int64  `json:"expires_at"`
}

type rateState struct {
	tokens float64
	last   time.Time
}

type Service struct {
	cfg Config

	ctx      context.Context
	cancel   context.CancelFunc
	identity *corecrypto.Identity
	node     *p2ptransport.Node

	stateMu    sync.RWMutex
	neighbors  map[string]PeerPresence
	seen       map[string]time.Time
	seenOrder  []string
	unread     map[string]int
	rateStates map[string]*rateState

	subMu       sync.RWMutex
	subscribers map[chan Event]struct{}

	wg sync.WaitGroup
}

func New(config Config) (*Service, error) {
	identity, err := corecrypto.LoadOrCreateIdentity()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		cfg: Config{
			LocalID: identity.PeerID,
			Port:    config.Port,
		},
		ctx:         ctx,
		cancel:      cancel,
		identity:    identity,
		neighbors:   make(map[string]PeerPresence),
		seen:        make(map[string]time.Time),
		unread:      make(map[string]int),
		rateStates:  make(map[string]*rateState),
		subscribers: make(map[chan Event]struct{}),
	}, nil
}

func (s *Service) Start() error {
	if err := history.UpsertPeerAlias(s.cfg.LocalID, "You"); err != nil {
		return err
	}
	if err := history.TouchChat(history.ChatRecord{}); err != nil {
		return err
	}
	node, err := p2ptransport.NewNode(s.ctx, s.identity, s.handlePacket, s.handlePeer)
	if err != nil {
		return err
	}
	s.node = node
	if err := s.bootstrapContactHints(); err != nil {
		return err
	}
	s.wg.Add(1)
	go s.retryOutboxLoop()
	return nil
}

func (s *Service) Stop() {
	s.cancel()
	if s.node != nil {
		_ = s.node.Close()
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
			Strategy:  item.Strategy.String(),
		})
	}
	return messages, nil
}

func (s *Service) OpenPrivateChat(peerID, peerAddr, name string) (ChatSummary, error) {
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
	if peerAddr != "" && s.node != nil {
		_ = s.node.RememberHint(peerID, peerAddr)
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
	summary, err := s.chatSummaryByID(chatID)
	if err != nil {
		return ChatSummary{}, err
	}
	return summary, nil
}

func (s *Service) SendMessage(chatID, targetID, text string) (UIMessage, error) {
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
		Strategy:  protocol.StrategyUnknown,
		TTL:       defaultHopTTL,
		TargetID:  targetID,
		ChatID:    chatID,
		From:      s.cfg.LocalID,
		Payload:   []byte(text),
		Timestamp: time.Now().UnixMilli(),
	}
	if err := msg.Sign(s.identity.PrivateKey); err != nil {
		return UIMessage{}, err
	}

	strategy, err := s.routeMessage(msg)
	if err != nil {
		return UIMessage{}, err
	}
	msg.Strategy = strategy

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

	ui := UIMessage{
		MessageID: msg.ID,
		ChatID:    msg.ChatID,
		TargetID:  msg.TargetID,
		From:      msg.From,
		Text:      text,
		Timestamp: msg.Timestamp,
		Direction: "outgoing",
		Strategy:  strategy.String(),
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
	if err := history.UpsertPeerAlias(created.PeerID, created.Name); err != nil {
		return corechat.Contact{}, err
	}
	if s.node != nil {
		_ = s.node.RememberHint(created.PeerID, created.Address())
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
	if err := history.UpsertPeerAlias(updated.PeerID, updated.Name); err != nil {
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
	return fmt.Errorf("peer_id is derived from .identity and cannot be changed from UI")
}

func (s *Service) GetInviteCode() (InviteCode, error) {
	code := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	rec, err := s.node.PublishInvite(code, defaultInviteTTL)
	if err != nil {
		return InviteCode{}, err
	}
	return InviteCode{
		Code:      code,
		PeerID:    rec.PeerID,
		ExpiresAt: rec.ExpiresAt,
	}, nil
}

func (s *Service) ResolveInviteCode(code string) (string, error) {
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()

	peerID, err := s.node.ResolveInvite(ctx, code)
	if err != nil {
		return "", fmt.Errorf("код не найден или сеть недоступна: %w", err)
	}
	return peerID, nil
}

func (s *Service) routeMessage(msg protocol.Message) (protocol.Strategy, error) {
	type result struct {
		strategy protocol.Strategy
		err      error
	}

	hopCh := make(chan result, 1)
	go func() {
		hopCtx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		hopCh <- result{strategy: protocol.StrategyHop, err: s.node.SendHop(hopCtx, msg, "")}
	}()

	directCtx, cancel := context.WithTimeout(s.ctx, relayFallbackTimeout)
	defer cancel()
	if strategy, err := s.node.SendDirect(directCtx, msg, false); err == nil {
		return strategy, nil
	}

	if strategy, err := s.node.SendDirect(s.ctx, msg, true); err == nil {
		return strategy, nil
	}

	select {
	case hopResult := <-hopCh:
		if hopResult.err == nil {
			return hopResult.strategy, nil
		}
	default:
	}

	msg.Strategy = protocol.StrategyOffline
	if err := history.QueueMessage(msg, time.Now().Add(10*time.Second).UnixMilli()); err != nil {
		return protocol.StrategyUnknown, err
	}
	return protocol.StrategyOffline, nil
}

func (s *Service) handlePacket(msg protocol.Message, sender peer.AddrInfo) {
	now := time.Now()
	if blocked, err := corechat.IsBlocked(msg.From); err != nil {
		s.emitError(err)
		return
	} else if blocked {
		return
	}
	if !s.allowRate(sender.ID.String(), now, 20, 40) {
		return
	}
	s.registerNeighbor(msg.From, bestAddr(sender), msg.From)

	switch msg.Type {
	case protocol.MsgJoin, protocol.MsgJoinAck:
		if summary, err := s.chatSummaryByID(msg.ChatID); err == nil {
			s.emit(Event{
				Type:      "chat_updated",
				Timestamp: now.UnixMilli(),
				Chat:      &summary,
			})
		}
	case protocol.MsgChat:
		if s.isSeen(msg.ID, now, 10*time.Minute) {
			return
		}
		if msg.TargetID != s.cfg.LocalID {
			if msg.Strategy != protocol.StrategyHop || msg.TTL <= 1 {
				return
			}
			msg.TTL--
			_ = s.node.SendHop(s.ctx, msg, sender.ID.String())
			return
		}
		if err := history.SaveMessage(msg); err != nil {
			s.emitError(err)
			return
		}
		if err := history.TouchChat(history.ChatRecord{
			ChatID:        msg.ChatID,
			PeerID:        msg.From,
			Title:         s.lookupPeerTitle(msg.From),
			LastMessage:   string(msg.Payload),
			LastTimestamp: msg.Timestamp,
		}); err != nil {
			s.emitError(err)
		}

		s.stateMu.Lock()
		s.unread[msg.ChatID]++
		s.stateMu.Unlock()

		ui := UIMessage{
			MessageID: msg.ID,
			ChatID:    msg.ChatID,
			TargetID:  msg.TargetID,
			From:      msg.From,
			Text:      string(msg.Payload),
			Timestamp: msg.Timestamp,
			Direction: "incoming",
			Strategy:  msg.Strategy.String(),
		}
		summary, _ := s.chatSummaryByID(msg.ChatID)
		s.emit(Event{
			Type:      "message_received",
			Timestamp: now.UnixMilli(),
			Message:   &ui,
			Chat:      &summary,
		})
	}
}

func (s *Service) handlePeer(info peer.AddrInfo) {
	if info.ID.String() == s.cfg.LocalID {
		return
	}
	s.registerNeighbor(info.ID.String(), bestAddr(info), info.ID.String())
}

func (s *Service) registerNeighbor(peerID, addr, name string) {
	peerID = strings.TrimSpace(peerID)
	addr = strings.TrimSpace(addr)
	if peerID == "" || peerID == s.cfg.LocalID {
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
	if name, err := history.LookupPeerAlias(peerID); err == nil && name != "" {
		return name
	}
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

	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	chats := make([]ChatSummary, 0, len(records))
	for _, record := range records {
		addr := ""
		if item, ok := contactMap[record.PeerID]; ok {
			addr = item.Address()
		} else if neighbor, ok := s.neighbors[record.PeerID]; ok {
			addr = neighbor.Addr
		}
		title := strings.TrimSpace(record.Title)
		if alias := s.lookupPeerName(record.PeerID); alias != "" {
			title = alias
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
	s.seenOrder = append(s.seenOrder, id)
	for len(s.seenOrder) > dedupCapacity {
		oldest := s.seenOrder[0]
		s.seenOrder = s.seenOrder[1:]
		delete(s.seen, oldest)
	}
	for key, ts := range s.seen {
		if now.Sub(ts) > ttl {
			delete(s.seen, key)
		}
	}
	return false
}

func (s *Service) retryOutboxLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.retryDueOutbox()
		}
	}
}

func (s *Service) retryDueOutbox() {
	items, err := history.LoadDueOutbox(time.Now().UnixMilli(), 16)
	if err != nil {
		s.emitError(err)
		return
	}
	for _, item := range items {
		msg := protocol.Message{
			Version:   protocol.ProtocolVersion,
			Type:      protocol.MsgChat,
			Target:    protocol.TargetPeer,
			TTL:       item.TTL,
			TargetID:  item.TargetID,
			ChatID:    item.ChatID,
			From:      s.cfg.LocalID,
			Payload:   item.Payload,
			Timestamp: item.CreatedAt,
		}
		if err := msg.Sign(s.identity.PrivateKey); err != nil {
			_ = history.UpdateOutboxFailure(item.MessageID, err.Error(), time.Now().Add(30*time.Second).UnixMilli())
			continue
		}
		msg.ID = item.MessageID
		strategy, err := s.routeMessage(msg)
		if err != nil {
			_ = history.UpdateOutboxFailure(item.MessageID, err.Error(), time.Now().Add(30*time.Second).UnixMilli())
			continue
		}
		msg.Strategy = strategy
		_ = history.SaveMessage(msg)
		_ = history.DeleteOutbox(item.MessageID)
	}
}

func (s *Service) bootstrapContactHints() error {
	contacts, err := corechat.ListContacts()
	if err != nil {
		return err
	}
	for _, item := range contacts {
		if item.Name != "" {
			_ = history.UpsertPeerAlias(item.PeerID, item.Name)
		}
		if s.node != nil {
			_ = s.node.RememberHint(item.PeerID, item.Address())
		}
	}
	return nil
}

func bestAddr(info peer.AddrInfo) string {
	if len(info.Addrs) == 0 {
		return ""
	}
	for _, addr := range info.Addrs {
		if !strings.Contains(addr.String(), "/p2p-circuit") {
			return addr.String()
		}
	}
	return info.Addrs[0].String()
}
func privateChatID(a, b string) string {
	if a <= b {
		return "dm:" + a + ":" + b
	}
	return "dm:" + b + ":" + a
}

var _ = errors.Is
