package cli

import (
	corechat "Syne/core/chat"
	"Syne/core/discovery"
	"Syne/core/history"
	"Syne/core/protocol"
	"Syne/core/transport"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Config contains minimal settings for local P2P chat testing.
type Config struct {
	LocalPort int
	LocalID   string
	PeerID    string
	PeerAddr  string
}

type rateState struct {
	tokens float64
	last   time.Time
}

const defaultHopTTL = 4

func allowRate(states map[string]*rateState, key string, now time.Time, ratePerSec, burst float64) bool {
	s, ok := states[key]
	if !ok {
		states[key] = &rateState{tokens: burst - 1, last: now}
		return true
	}
	elapsed := now.Sub(s.last).Seconds()
	s.tokens += elapsed * ratePerSec
	if s.tokens > burst {
		s.tokens = burst
	}
	s.last = now
	if s.tokens < 1 {
		return false
	}
	s.tokens -= 1
	return true
}

// RunChat starts a terminal chat over TCP with protocol.Message framing.
func RunChat(ctx context.Context, cfg Config) error {
	if cfg.LocalPort <= 0 {
		return fmt.Errorf("local port must be > 0")
	}
	if strings.TrimSpace(cfg.LocalID) == "" {
		cfg.LocalID = "peer"
	}

	listener, err := transport.ListenTCP(cfg.LocalPort)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}
	defer listener.Close()

	reader := bufio.NewReader(os.Stdin)
	var stateMu sync.RWMutex
	var activePeer *transport.Peer
	var activeChatID string
	knownChats := make(map[string]struct{})
	seenMessages := make(map[string]time.Time)
	rateStates := make(map[string]*rateState)
	var rateMu sync.Mutex
	neighbors := make(map[string]string)
	var neighborsMu sync.RWMutex

	openChat := func(peerID, peerAddr string) error {
		peerID = strings.TrimSpace(peerID)
		peerAddr = strings.TrimSpace(peerAddr)
		if peerID == "" || peerAddr == "" {
			return fmt.Errorf("peer-id and peer-addr are required")
		}
		if blocked, err := corechat.IsBlocked(peerID); err != nil {
			return err
		} else if blocked {
			return fmt.Errorf("peer is blocked: %s", peerID)
		}
		addr, err := net.ResolveTCPAddr("tcp", peerAddr)
		if err != nil {
			return fmt.Errorf("resolve peer address: %w", err)
		}

		peer := &transport.Peer{PeerID: peerID, Addr: addr}
		chatID := privateChatID(cfg.LocalID, peerID)

		stateMu.Lock()
		activePeer = peer
		activeChatID = chatID
		knownChats[chatID] = struct{}{}
		stateMu.Unlock()

		fmt.Printf("[system] active chat -> %s (%s) chatID=%s\n", peerID, peerAddr, chatID)
		if messages, err := history.LoadMessages(chatID); err != nil {
			fmt.Printf("history load error: %v\n", err)
		} else {
			for _, histMsg := range messages {
				fmt.Printf("\n<- [history] [%s]: %s - %s\n> ", chatID, histMsg.From, string(histMsg.Payload))
			}
		}

		join := protocol.NewJoin(chatID, cfg.LocalID, peerID)
		if err := sendProtocolMessage(peer, join); err != nil {
			fmt.Printf("join send error: %v\n", err)
		}
		return nil
	}

	fmt.Printf("Chat started. local=%s, me=%s\n", listener.Addr().String(), cfg.LocalID)
	fmt.Println("/help - show commands")
	fmt.Println("Then type text to send into active chat. Ctrl+C to exit.")

	if err := discovery.StartLANDiscovery(ctx, cfg.LocalID, cfg.LocalPort, func(peerID, addr string) {
		neighborsMu.Lock()
		neighbors[peerID] = addr
		neighborsMu.Unlock()
	}); err != nil {
		fmt.Printf("discovery error: %v\n", err)
	}

	if strings.TrimSpace(cfg.PeerID) != "" && strings.TrimSpace(cfg.PeerAddr) != "" {
		if err := openChat(cfg.PeerID, cfg.PeerAddr); err != nil {
			fmt.Printf("initial chat open error: %v\n", err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, sender, err := transport.AcceptTCP(listener)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				fmt.Printf("accept error: %v\n", err)
				return
			}

			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			data, err := transport.ReceiveTCP(conn, 64*1024)
			if err != nil {
				fmt.Printf("receive error from %s: %v\n", sender.String(), err)
				continue
			}

			msg, err := protocol.UnmarshalMessage(data)
			if err != nil {
				fmt.Printf("decode error from %s: %v\n", sender.String(), err)
				continue
			}
			if err := protocol.ValidateMessage(msg); err != nil {
				fmt.Printf("invalid message from %s: %v\n", sender.String(), err)
				continue
			}

			switch msg.Type {
			case protocol.MsgJoin:
				if blocked, err := corechat.IsBlocked(msg.From); err != nil {
					fmt.Printf("\n[system] blocklist check error: %v\n> ", err)
					continue
				} else if blocked {
					fmt.Printf("\n[system] blocked peer %s tried to join (dropped)\n> ", msg.From)
					continue
				}
				rateMu.Lock()
				allowed := allowRate(rateStates, sender.IP.String(), time.Now(), 8, 16)
				rateMu.Unlock()
				if !allowed {
					fmt.Printf("\n[system] rate limit exceeded for %s (join)\n> ", sender.IP.String())
					continue
				}
				stateMu.Lock()
				knownChats[msg.ChatID] = struct{}{}
				stateMu.Unlock()
				fmt.Printf("\n[system] %s joined chat %s\n> ", msg.From, msg.ChatID)
				ack := protocol.NewJoinAck(msg.ChatID, cfg.LocalID, msg.From)
				if err := sendAckToContact(ack, sender); err != nil {
					fmt.Printf("join ack send error: %v\n", err)
				}
			case protocol.MsgJoinAck:
				if blocked, err := corechat.IsBlocked(msg.From); err != nil {
					fmt.Printf("\n[system] blocklist check error: %v\n> ", err)
					continue
				} else if blocked {
					fmt.Printf("\n[system] blocked peer %s join-ack (dropped)\n> ", msg.From)
					continue
				}
				stateMu.Lock()
				knownChats[msg.ChatID] = struct{}{}
				stateMu.Unlock()
				fmt.Printf("\n[system] %s acknowledged join for %s\n> ", msg.From, msg.ChatID)
			case protocol.MsgChat:
				if msg.MessageID == "" {
					fmt.Printf("\n[system] missing message_id from %s (dropped)\n> ", msg.From)
					continue
				}
				if blocked, err := corechat.IsBlocked(msg.From); err != nil {
					fmt.Printf("\n[system] blocklist check error: %v\n> ", err)
					continue
				} else if blocked {
					fmt.Printf("\n[system] message from blocked peer %s (dropped)\n> ", msg.From)
					continue
				}

				rateMu.Lock()
				allowed := allowRate(rateStates, sender.IP.String(), time.Now(), 20, 40)
				rateMu.Unlock()
				if !allowed {
					fmt.Printf("\n[system] rate limit exceeded for %s\n> ", sender.IP.String())
					continue
				}

				if isSeen(seenMessages, msg.MessageID, time.Now(), 10*time.Minute) {
					continue
				}

				if msg.TargetID == cfg.LocalID {
					hopsTaken := defaultHopTTL - msg.HopTTL
					fmt.Printf("\n<- %s [%s] (via %d hops, last relay: %s): %s\n> ", msg.From, msg.ChatID, hopsTaken, sender.IP.String(), string(msg.Payload))
					if err := history.SaveMessage(msg); err != nil {
						fmt.Printf("history save error: %v\n", err)
					}
				} else {
					if msg.HopTTL <= 1 {
						fmt.Printf("\n[system] TTL expired for msg %s from %s\n> ", msg.MessageID, msg.From)
						continue
					}
					msg.HopTTL--
					fmt.Printf("\n[system] Relay: forwarding msg from %s to %s (TTL: %d)\n> ", msg.From, msg.TargetID, msg.HopTTL)
					if err := forwardToNeighbors(msg, sender, neighbors, &neighborsMu); err != nil {
						fmt.Printf("\n[system] forward error: %v\n> ", err)
					}
				}
			default:
				fmt.Printf("\n[system] %s [%s] from %s\n> ", msg.Type.String(), msg.ChatID, msg.From)
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			_ = listener.Close()
			wg.Wait()
			return nil
		default:
		}

		fmt.Print("> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = listener.Close()
				wg.Wait()
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}

		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "/") {
			if err := handleCommand(text, openChat, cfg.LocalID, neighbors, &neighborsMu); err != nil {
				fmt.Printf("command error: %v\n", err)
			}
			continue
		}

		stateMu.RLock()
		peer := activePeer
		chatID := activeChatID
		stateMu.RUnlock()
		if peer == nil || chatID == "" {
			fmt.Println("no active chat. Use /chat open <name-or-peer-id> first")
			continue
		}
		if blocked, err := corechat.IsBlocked(peer.PeerID); err != nil {
			fmt.Printf("blocklist check error: %v\n", err)
			continue
		} else if blocked {
			fmt.Printf("[system] can't send: peer is blocked: %s\n", peer.PeerID)
			continue
		}

		out := protocol.Message{
			Version:   protocol.ProtocolVersion,
			Type:      protocol.MsgChat,
			Target:    protocol.TargetPeer,
			MessageID: uuid.NewString(),
			HopTTL:    defaultHopTTL,
			TargetID:  peer.PeerID,
			ChatID:    chatID,
			From:      cfg.LocalID,
			Payload:   []byte(text),
			Timestamp: time.Now().UnixMilli(),
		}

		if err := sendProtocolMessage(peer, out); err != nil {
			fmt.Printf("send error: %v\n", err)
			continue
		}
		if err := history.SaveMessage(out); err != nil {
			fmt.Printf("history save (outgoing) error: %v\n", err)
		}
	}
}

func forwardToNeighbors(msg protocol.Message, sender *net.TCPAddr, neighbors map[string]string, neighborsMu *sync.RWMutex) error {
	contacts, err := corechat.ListContacts()
	if err != nil {
		return err
	}

	targets := make(map[string]*transport.Peer)
	count := 0
	addTarget := func(peerID, addrStr string) {
		if peerID == msg.From {
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

	for _, c := range contacts {
		addTarget(c.PeerID, c.Address())
	}

	if neighborsMu != nil {
		neighborsMu.RLock()
		for peerID, addrStr := range neighbors {
			addTarget(peerID, addrStr)
		}
		neighborsMu.RUnlock()
	}

	for _, peer := range targets {
		count++
		go func(p *transport.Peer, m protocol.Message) {
			_ = sendProtocolMessage(p, m)
		}(peer, msg)
	}
	if count == 0 {
		return fmt.Errorf("no neighbors or contacts to forward")
	}
	return nil
}

func isSeen(cache map[string]time.Time, id string, now time.Time, ttl time.Duration) bool {
	if id == "" {
		return false
	}
	if ts, ok := cache[id]; ok {
		if now.Sub(ts) <= ttl {
			return true
		}
	}
	cache[id] = now
	for k, v := range cache {
		if now.Sub(v) > ttl {
			delete(cache, k)
		}
	}
	return false
}

func handleCommand(input string, openChat func(peerID, peerAddr string) error, localID string, neighbors map[string]string, neighborsMu *sync.RWMutex) error {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "/help":
		fmt.Println("/contact add <name> <peer-id> <ip:port>      - добавить контакт")
		fmt.Println("/contact rename <name-or-peer-id> <new-name>  - переименовать контакт")
		fmt.Println("/contact delete <name-or-peer-id>             - удалить контакт")
		fmt.Println("/contact list                                 - показать список контактов")
		fmt.Println("/block add <name-or-peer-id> [reason]          - добавить в ЧС")
		fmt.Println("/block remove <name-or-peer-id>               - убрать из ЧС")
		fmt.Println("/block list                                   - показать ЧС")
		fmt.Println("/chat open <name-or-peer-id>                  - открыть чат с контактом")
		fmt.Println("/sendto <peer-id> <text>                      - отправить сообщение выбранному peer")
		return nil
	case "/contact":
		if len(parts) < 2 {
			return fmt.Errorf("usage: /contact add <name> <peer-id> <ip:port> | /contact list")
		}
		switch parts[1] {
		case "add":
			if len(parts) != 5 {
				return fmt.Errorf("usage: /contact add <name> <peer-id> <ip:port>")
			}
			host, port, err := net.SplitHostPort(parts[4])
			if err != nil {
				return fmt.Errorf("invalid address: %w", err)
			}
			return corechat.AddContact(corechat.Contact{
				Name:   parts[2],
				PeerID: parts[3],
				IP:     host,
				Port:   port,
			})
		case "rename":
			if len(parts) != 4 {
				return fmt.Errorf("usage: /contact rename <name-or-peer-id> <new-name>")
			}
			err := corechat.RenameContact(parts[2], parts[3])
			if err != nil {
				return err
			}
			return nil
		case "delete":
			if len(parts) != 3 {
				return fmt.Errorf("usage: /contact delete <name-or-peer-id>")
			}

			err := corechat.DeleteContact(parts[2])
			if err != nil {
				return err
			}
			return nil
		case "list":
			contacts, err := corechat.ListContacts()
			if err != nil {
				return err
			}
			if len(contacts) == 0 {
				fmt.Println("[system] contacts are empty")
				return nil
			}
			for _, c := range contacts {
				fmt.Printf("[contact] %s | %s | %s\n", c.Name, c.PeerID, c.Address())
			}
			return nil
		default:
			return fmt.Errorf("unknown /contact command")
		}

	case "/chat":
		if len(parts) != 3 || parts[1] != "open" {
			return fmt.Errorf("usage: /chat open <name-or-peer-id>")
		}
		c, err := corechat.FindContact(parts[2])
		if err != nil {
			return err
		}
		return openChat(c.PeerID, c.Address())
	case "/sendto":
		if len(parts) < 3 {
			return fmt.Errorf("usage: /sendto <peer-id> <text>")
		}
		targetID := parts[1]
		if blocked, err := corechat.IsBlocked(targetID); err != nil {
			return err
		} else if blocked {
			return fmt.Errorf("peer is blocked: %s", targetID)
		}
		text := strings.Join(parts[2:], " ")
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("text is required")
		}
		msg := protocol.Message{
			Version:   protocol.ProtocolVersion,
			Type:      protocol.MsgChat,
			Target:    protocol.TargetPeer,
			MessageID: uuid.NewString(),
			HopTTL:    defaultHopTTL,
			TargetID:  targetID,
			ChatID:    privateChatID(localID, targetID),
			From:      localID,
			Payload:   []byte(text),
			Timestamp: time.Now().UnixMilli(),
		}
		if err := forwardToNeighbors(msg, nil, neighbors, neighborsMu); err != nil {
			return err
		}
		return nil
	case "/block":
		if len(parts) < 2 {
			return fmt.Errorf("usage: /block add <name-or-peer-id> [reason] | /block remove <name-or-peer-id> | /block list")
		}
		switch parts[1] {
		case "add":
			if len(parts) < 3 {
				return fmt.Errorf("usage: /block add <name-or-peer-id> [reason]")
			}
			reason := ""
			if len(parts) > 3 {
				reason = strings.Join(parts[3:], " ")
			}
			return corechat.AddBlocked(parts[2], reason)
		case "remove":
			if len(parts) != 3 {
				return fmt.Errorf("usage: /block remove <name-or-peer-id>")
			}
			return corechat.RemoveBlocked(parts[2])
		case "list":
			items, err := corechat.ListBlocked()
			if err != nil {
				return err
			}
			if len(items) == 0 {
				fmt.Println("[system] blocklist is empty")
				return nil
			}
			for _, it := range items {
				label := it.PeerID
				if strings.TrimSpace(it.Name) != "" {
					label = it.Name + " | " + it.PeerID
				}
				if strings.TrimSpace(it.Reason) != "" {
					fmt.Printf("[blocked] %s | reason: %s\n", label, it.Reason)
				} else {
					fmt.Printf("[blocked] %s\n", label)
				}
			}
			return nil
		default:
			return fmt.Errorf("unknown /block command")
		}
	default:
		return fmt.Errorf("unknown command")
	}

}

func sendProtocolMessage(peer *transport.Peer, msg protocol.Message) error {
	if err := protocol.ValidateMessage(msg); err != nil {
		return fmt.Errorf("message build error: %w", err)
	}
	wire, err := protocol.MarshalMessage(msg)
	if err != nil {
		return fmt.Errorf("encode error: %w", err)
	}
	if err := transport.SendTCP(peer, wire); err != nil {
		return fmt.Errorf("transport send: %w", err)
	}
	return nil
}

func sendAckToContact(msg protocol.Message, sender *net.TCPAddr) error {
	c, err := corechat.FindContact(msg.TargetID)
	if err == nil {
		addr, err := net.ResolveTCPAddr("tcp", c.Address())
		if err != nil {
			return fmt.Errorf("ack resolve address: %w", err)
		}
		peer := &transport.Peer{PeerID: c.PeerID, Addr: addr}
		return sendProtocolMessage(peer, msg)
	}

	// Fallback: send back to the actual sender if contact is missing.
	if sender == nil {
		return fmt.Errorf("ack contact not found and sender is nil: %w", err)
	}
	peer := &transport.Peer{PeerID: msg.TargetID, Addr: sender}
	return sendProtocolMessage(peer, msg)
}

func privateChatID(a, b string) string {
	if a <= b {
		return "dm:" + a + ":" + b
	}
	return "dm:" + b + ":" + a
}
