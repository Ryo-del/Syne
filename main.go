package main

import (
	"Syne/cli"
	"Syne/core/chat"
	"Syne/core/transport"

	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
)

var ID string
var PeerAddr string
var PeerID string
var Port int

/*
--id = мой ID - flag
--port = на каком порту я слушаю - auto
--peer-id = ID собеседника - flag
--peer-addr = адрес собеседника (IP:port) - flag
*/
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	flag.StringVar(&ID, "id", "", "Your peer ID")
	flag.IntVar(&Port, "port", 3000, "Local TCP port (auto-picks next free if busy)")
	flag.StringVar(&PeerID, "peer-id", "peer", "Peer ID to chat with")
	flag.StringVar(&PeerAddr, "peer-addr", "", "Peer address (IP:port)")
	flag.Parse()
	existingID, _ := chat.GetUserID()
	if ID == "" && existingID == "" {
		fmt.Print("Enter your id: ")
		fmt.Scan(&ID)

		if ID == "" {

			ID = uuid.NewString()
			fmt.Println("Generated auto-ID:", ID)
		}

		err := chat.SaveUserData(ID)
		if err != nil {
			fmt.Println("Error saving ID:", err)
		}
	} else if ID == "" {
		ID = existingID
	}

	port := Port
	for !transport.IsPortFree(port) {
		port++
	}
	fmt.Printf("Using local port: %d\n", port)
	fmt.Printf("Local peer ID: %s\n", ID)

	done := make(chan struct{})
	go func() {
		defer close(done)
		err := cli.RunChat(ctx, cli.Config{
			LocalPort: port,
			PeerID:    PeerID,
			LocalID:   ID,
			PeerAddr:  PeerAddr,
		})
		if err != nil {
			log.Default().Printf("Chat error: %v", err)
		}
	}()
	<-ctx.Done()
	fmt.Println("Shutting down gracefully...")
	<-done
}
