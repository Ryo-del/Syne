package main

import (
	"Sync/core/transport"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	port := 3000

	conn, err := transport.ListenUDP(port)
	if err != nil {
		panic(err)
	}
	addr, err := net.ResolveUDPAddr("udp6", "[::1]:3000")
	if err != nil {
		panic(err)
	}
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			data, sender, err := transport.ReceptionUDP(conn)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				fmt.Println("Error receiving UDP:", err)
				return
			}

			fmt.Printf("Received %s from %s\n", string(data), sender)
		}
	}()
	err = transport.SendUDP(conn, &transport.Peer{ID: "peer1", Addr: addr}, []byte("Ping"))
	if err != nil {
		log.Default().Printf("Error sending UDP: %v", err)
	}
	<-ctx.Done()
	fmt.Println("Shutting down gracefully...")
	_ = conn.Close()
	<-done
}
