package cli

import (
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
)

// Config contains minimal settings for local P2P chat testing.
type Config struct {
	LocalPort int
	PeerID    string
	PeerAddr  string
}

// RunChat starts a terminal chat that talks directly to core transport.
// Type text and press Enter to send. Ctrl+C should be handled by caller context.
func RunChat(ctx context.Context, cfg Config) error {
	if cfg.LocalPort <= 0 {
		return fmt.Errorf("local port must be > 0")
	}
	if strings.TrimSpace(cfg.PeerID) == "" {
		cfg.PeerID = "peer"
	}
	if strings.TrimSpace(cfg.PeerAddr) == "" {
		return fmt.Errorf("peer address is required")
	}

	peerAddr, err := net.ResolveUDPAddr("udp6", cfg.PeerAddr)
	if err != nil {
		return fmt.Errorf("resolve peer address: %w", err)
	}

	conn, err := transport.ListenUDP(cfg.LocalPort)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer conn.Close()

	peer := &transport.Peer{PeerID: cfg.PeerID, Addr: peerAddr}
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("Chat started. local=%s, peer=%s (%s)\n", conn.LocalAddr().String(), peer.PeerID, peer.Addr.String())
	fmt.Println("Type message + Enter. Ctrl+C to exit.")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			data, sender, err := transport.ReceptionUDP(conn)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				fmt.Printf("receive error: %v\n", err)
				return
			}
			fmt.Printf("\n<- %s: %s\n> ", sender.String(), string(data))
		}
	}()

	for {
		select {
		case <-ctx.Done():
			_ = conn.Close()
			wg.Wait()
			return nil
		default:
		}

		fmt.Print("> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = conn.Close()
				wg.Wait()
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}

		msg := strings.TrimSpace(line)
		if msg == "" {
			continue
		}

		err = transport.SendUDP(conn, peer, []byte(msg))
		if err != nil {
			fmt.Printf("send error: %v\n", err)
		}
	}
}
