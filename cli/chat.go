package cli

import (
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
)

// Config contains minimal settings for local P2P chat testing.
type Config struct {
	LocalPort int
	LocalID   string
	PeerID    string
	PeerAddr  string
}

// RunChat starts a terminal chat over TCP with protocol.Message framing.
func RunChat(ctx context.Context, cfg Config) error {
	if cfg.LocalPort <= 0 {
		return fmt.Errorf("local port must be > 0")
	}
	if strings.TrimSpace(cfg.LocalID) == "" {
		cfg.LocalID = "peer"
	}
	if strings.TrimSpace(cfg.PeerID) == "" {
		cfg.PeerID = "peer"
	}
	if strings.TrimSpace(cfg.PeerAddr) == "" {
		return fmt.Errorf("peer address is required")
	}

	peerAddr, err := net.ResolveTCPAddr("tcp", cfg.PeerAddr)
	if err != nil {
		return fmt.Errorf("resolve peer address: %w", err)
	}

	listener, err := transport.ListenTCP(cfg.LocalPort)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}
	defer listener.Close()

	peer := &transport.Peer{PeerID: cfg.PeerID, Addr: peerAddr}
	chatID := privateChatID(cfg.LocalID, cfg.PeerID)
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("Chat started. local=%s, me=%s, peer=%s (%s)\n", listener.Addr().String(), cfg.LocalID, peer.PeerID, peer.Addr.String())
	fmt.Println("Type message + Enter. Ctrl+C to exit.")
	if messages, err := history.LoadMessages(chatID); err != nil {
		fmt.Printf("history load error: %v\n", err)
	} else {
		for _, histMsg := range messages {
			fmt.Printf("\n<- [history] [%s]: %s - %s\n> ", chatID, histMsg.From, string(histMsg.Payload))
		}
	}

	join := protocol.NewJoin(chatID, cfg.LocalID, cfg.PeerID)
	if err := sendProtocolMessage(peer, join); err != nil {
		fmt.Printf("join send error: %v\n", err)
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

			data, err := transport.ReceiveTCP(conn)
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
				fmt.Printf("\n[system] %s joined chat %s\n> ", msg.From, msg.ChatID)
				ack := protocol.NewJoinAck(msg.ChatID, cfg.LocalID, msg.From)
				if err := sendProtocolMessage(peer, ack); err != nil {
					fmt.Printf("join ack send error: %v\n", err)
				}

			case protocol.MsgJoinAck:
				fmt.Printf("\n[system] %s acknowledged join for %s\n> ", msg.From, msg.ChatID)
			case protocol.MsgChat:
				fmt.Printf("\n<- %s [%s]: %s\n> ", msg.From, msg.ChatID, string(msg.Payload))
				if err := history.SaveMessage(msg.ChatID, msg.Payload, msg.Version, uint8(msg.Type), uint8(msg.Target), msg.TargetID, msg.From); err != nil {
					fmt.Printf("history save (incoming) error: %v\n", err)
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

		out := protocol.Message{
			Version:   protocol.ProtocolVersion,
			Type:      protocol.MsgChat,
			Target:    protocol.TargetPeer,
			TargetID:  cfg.PeerID,
			ChatID:    chatID,
			From:      cfg.LocalID,
			Payload:   []byte(text),
			Timestamp: time.Now().UnixMilli(),
		}

		if err := sendProtocolMessage(peer, out); err != nil {
			fmt.Printf("send error: %v\n", err)
			continue
		}
		if err := history.SaveMessage(out.ChatID, out.Payload, out.Version, uint8(out.Type), uint8(out.Target), out.TargetID, out.From); err != nil {
			fmt.Printf("history save (outgoing) error: %v\n", err)
		}
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

func privateChatID(a, b string) string {
	if a <= b {
		return "dm:" + a + ":" + b
	}
	return "dm:" + b + ":" + a
}
