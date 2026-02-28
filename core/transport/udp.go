package transport

import (
	"fmt"
	"net"
)

// UDPTransport is a base point for direct UDP packet exchange.
type Peer struct {
	PeerID string
	Addr   *net.UDPAddr
}

func ListenUDP(port int) (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp6", &net.UDPAddr{
		Port: port,
	})
	if err != nil {
		return nil, err
	}
	fmt.Println("Listening on port", conn.LocalAddr().(*net.UDPAddr).Port)
	return conn, nil
}

func SendUDP(conn *net.UDPConn, peer *Peer, data []byte) error {
	if peer == nil || peer.Addr == nil {
		return fmt.Errorf("peer address is nil")
	}
	_, err := conn.WriteToUDP(data, peer.Addr)
	return err
}

func ReceptionUDP(conn *net.UDPConn) ([]byte, *net.UDPAddr, error) {
	buf := make([]byte, 1024)
	n, sender, err := conn.ReadFromUDP(buf)
	if err != nil {
		return nil, nil, err
	}
	return buf[:n], sender, nil
}

func IsPortFree(port int) bool {
	conn, err := net.ListenUDP("udp6", &net.UDPAddr{
		Port: port,
	})
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
