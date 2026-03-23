package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	DiscoveryPort  = 30100
	DiscoveryMagic = "SYNE1"
)

// StartLANDiscovery broadcasts our presence and listens for peers on the LAN.
// onPeer is called with (peerID, addrString) where addrString is "ip:port".
func StartLANDiscovery(
	ctx context.Context,
	localID string,
	tcpPort int,
	onPeer func(peerID, addr string),
) error {
	if onPeer == nil {
		return fmt.Errorf("onPeer callback is required")
	}

	recvConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: DiscoveryPort})
	if err != nil {
		return err
	}

	sendConn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		_ = recvConn.Close()
		return err
	}

	go func() {
		<-ctx.Done()
		_ = recvConn.Close()
		_ = sendConn.Close()
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, sender, err := recvConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			msg := strings.TrimSpace(string(buf[:n]))
			parts := strings.Split(msg, "|")
			if len(parts) != 3 || parts[0] != DiscoveryMagic {
				continue
			}
			peerID := parts[1]
			if peerID == "" || peerID == localID {
				continue
			}
			port, err := strconv.Atoi(parts[2])
			if err != nil || port <= 0 {
				continue
			}
			addr := net.JoinHostPort(sender.IP.String(), strconv.Itoa(port))
			onPeer(peerID, addr)
		}
	}()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		dst := &net.UDPAddr{IP: net.IPv4bcast, Port: DiscoveryPort}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				payload := fmt.Sprintf(
					"%s|%s|%d",
					DiscoveryMagic,
					localID,
					tcpPort,
				)
				_, _ = sendConn.WriteToUDP([]byte(payload), dst)
			}
		}
	}()

	return nil
}
