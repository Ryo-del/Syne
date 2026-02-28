package main

import (
	"Syne/cli"
	"Syne/core/transport"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var ID string

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	port := 3000 // default port
	for !transport.IsPortFree(port) {
		port++
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		err := cli.RunChat(ctx, cli.Config{
			LocalPort: port,
			PeerID:    "peer2",
			PeerAddr:  "[::1]:3000",
		})
		if err != nil {
			log.Default().Printf("Chat error: %v", err)
		}
	}()
	<-ctx.Done()
	fmt.Println("Shutting down gracefully...")
	<-done
}
