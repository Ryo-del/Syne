package main

import (
	"Syne/core/app"
	corechat "Syne/core/chat"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type server struct {
	service *app.Service
}

func main() {
	var (
		addr    string
		localID string
		port    int
		workdir string
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:38673", "HTTP listen address")
	flag.StringVar(&localID, "id", "", "Local peer ID")
	flag.IntVar(&port, "port", 3000, "Preferred local TCP port")
	flag.StringVar(&workdir, "workdir", "", "Working directory for local data files")
	flag.Parse()

	if strings.TrimSpace(workdir) != "" {
		if err := os.MkdirAll(workdir, 0o755); err != nil {
			log.Fatalf("create workdir: %v", err)
		}
		if err := os.Chdir(workdir); err != nil {
			log.Fatalf("chdir workdir: %v", err)
		}
	}

	service, err := app.New(app.Config{
		LocalID: localID,
		Port:    port,
	})
	if err != nil {
		log.Fatalf("init service: %v", err)
	}
	if err := service.Start(); err != nil {
		log.Fatalf("start service: %v", err)
	}
	defer service.Stop()

	srv := &server{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", srv.handleHealth)
	mux.HandleFunc("/api/bootstrap", srv.handleBootstrap)
	mux.HandleFunc("/api/profile", srv.handleProfile)
	mux.HandleFunc("/api/events", srv.handleEvents)
	mux.HandleFunc("/api/chats/open", srv.handleOpenChat)
	mux.HandleFunc("/api/chats/read", srv.handleReadChat)
	mux.HandleFunc("/api/chats/", srv.handleChatRoutes)
	mux.HandleFunc("/api/messages", srv.handleSendMessage)
	mux.HandleFunc("/api/contacts", srv.handleContacts)
	mux.HandleFunc("/api/contacts/", srv.handleContactRoutes)
	mux.HandleFunc("/api/blocked", srv.handleBlocked)
	mux.HandleFunc("/api/blocked/", srv.handleBlockedRoutes)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("syne ui api listening on http://%s", addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http server: %v", err)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
	})
}

func (s *server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snapshot, err := s.service.Snapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *server) handleProfile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		snapshot, err := s.service.Snapshot()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"local_id": snapshot.LocalID,
		})
	case http.MethodPatch:
		var req struct {
			PeerID string `json:"peer_id"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(req.PeerID) != "" {
			if err := s.service.UpdateLocalPeerID(req.PeerID); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		snapshot, err := s.service.Snapshot()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"local_id": snapshot.LocalID,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsubscribe := s.service.Subscribe(64)
	defer unsubscribe()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\n", event.Type)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (s *server) handleOpenChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		PeerID   string `json:"peer_id"`
		PeerAddr string `json:"peer_addr"`
		Name     string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	summary, err := s.service.OpenPrivateChat(req.PeerID, req.PeerAddr, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *server) handleReadChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ChatID string `json:"chat_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.service.MarkChatRead(req.ChatID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleChatRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/chats/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeError(w, http.StatusNotFound, "chat route not found")
		return
	}
	if !strings.HasSuffix(path, "/messages") {
		writeError(w, http.StatusNotFound, "chat route not found")
		return
	}
	chatID := strings.TrimSuffix(path, "/messages")
	chatID = strings.Trim(chatID, "/")
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat_id is required")
		return
	}
	chatID, err := url.PathUnescape(chatID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := s.service.ListMessages(chatID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ChatID   string `json:"chat_id"`
		TargetID string `json:"target_id"`
		Text     string `json:"text"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	message, err := s.service.SendMessage(req.ChatID, req.TargetID, req.Text)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, message)
}

func (s *server) handleContacts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		snapshot, err := s.service.Snapshot()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, snapshot.Contacts)
	case http.MethodPost:
		var req corechat.Contact
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		contact, err := s.service.AddContact(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, contact)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *server) handleContactRoutes(w http.ResponseWriter, r *http.Request) {
	query, err := decodePathValue(r.URL.Path, "/api/contacts/")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := s.service.DeleteContact(query); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodPatch:
		var req struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		contact, err := s.service.RenameContact(query, req.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, contact)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *server) handleBlocked(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		snapshot, err := s.service.Snapshot()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, snapshot.Blocked)
	case http.MethodPost:
		var req struct {
			Query  string `json:"query"`
			Reason string `json:"reason"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := s.service.BlockPeer(req.Query, req.Reason)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *server) handleBlockedRoutes(w http.ResponseWriter, r *http.Request) {
	query, err := decodePathValue(r.URL.Path, "/api/blocked/")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.service.UnblockPeer(query); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func decodePathValue(path, prefix string) (string, error) {
	value := strings.TrimPrefix(path, prefix)
	value = strings.Trim(value, "/")
	if value == "" {
		return "", fmt.Errorf("path value is required")
	}
	decoded, err := urlPathUnescape(value)
	if err != nil {
		return "", err
	}
	return decoded, nil
}

func urlPathUnescape(value string) (string, error) {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", err
	}
	return decoded, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}
