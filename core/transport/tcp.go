package transport

import (
	"fmt"
	"io"
	"net"
)

// Peer contains logical peer identity and current TCP endpoint.
type Peer struct {
	PeerID string
	Addr   *net.TCPAddr
}

func ListenTCP(port int) (*net.TCPListener, error) {
	addr := &net.TCPAddr{Port: port}
	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return nil, err
	}
	fmt.Println("Listening on port", listener.Addr().(*net.TCPAddr).Port)
	return listener, nil
}

func SendTCP(peer *Peer, data []byte) error {
	if peer == nil || peer.Addr == nil {
		return fmt.Errorf("peer address is nil")
	}

	conn, err := net.DialTCP("tcp", nil, peer.Addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(data)
	return err
}

func AcceptTCP(listener *net.TCPListener) (net.Conn, *net.TCPAddr, error) {
	conn, err := listener.Accept()
	if err != nil {
		return nil, nil, err
	}

	addr, _ := conn.RemoteAddr().(*net.TCPAddr)
	return conn, addr, nil
}

func ReceiveTCP(conn net.Conn) ([]byte, error) {
	defer conn.Close()
	return io.ReadAll(conn)
}

func IsPortFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
