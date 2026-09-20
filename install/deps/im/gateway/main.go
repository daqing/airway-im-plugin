// The Airway IM Gateway maintains long-lived client WebSocket connections and
// delivers real-time IM events. It authenticates every connection against the
// backend's internal API and receives delivery commands from the delivery
// service; it contains no conversation business logic. See
// deps/im/docs/design/gateway.md for the wire protocol and architecture.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait     = 10 * time.Second
	pongWait      = 40 * time.Second
	pingPeriod    = 30 * time.Second
	authDeadline  = 10 * time.Second
	maxFrameBytes = 64 * 1024
)

type config struct {
	addr           string
	backendURL     string
	internalSecret string
	allowedOrigins map[string]struct{}
}

type metrics struct {
	accepted      atomic.Int64
	authenticated atomic.Int64
	authFailed    atomic.Int64
	closed        atomic.Int64
	delivered     atomic.Int64
	slowConsumers atomic.Int64
}

type hub struct {
	mu      sync.RWMutex
	users   map[string]map[*client]struct{}
	metrics metrics
}

type client struct {
	conn     *websocket.Conn
	send     chan []byte
	userUUID string
	close    sync.Once
}

type server struct {
	config config
	hub    *hub
	client *http.Client
}

type command struct {
	Cmd  string `json:"cmd"`
	Opts []any  `json:"opts"`
}

type envelope struct {
	Code    int    `json:"code"`
	Data    any    `json:"data"`
	Message string `json:"message,omitempty"`
}

func main() {
	cfg := loadConfig()
	s := &server{config: cfg, hub: &hub{users: make(map[string]map[*client]struct{})}, client: &http.Client{Timeout: 5 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.websocket)
	mux.HandleFunc("/internal/v1/deliver", s.deliver)
	mux.HandleFunc("/internal/v1/online", s.online)
	mux.HandleFunc("/internal/v1/kick", s.kick)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("/metrics", s.prometheus)
	mux.HandleFunc("/dashboard", s.dashboard)

	httpServer := &http.Server{Addr: cfg.addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("Airway IM Gateway listening on %s", cfg.addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

func loadConfig() config {
	origins := make(map[string]struct{})
	for _, value := range strings.Split(os.Getenv("GATEWAY_ALLOWED_ORIGINS"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			origins[value] = struct{}{}
		}
	}
	return config{addr: envOr("GATEWAY_ADDR", ":1910"), backendURL: strings.TrimRight(envOr("INTERNAL_SERVICE_URL", "http://127.0.0.1:1906"), "/"), internalSecret: os.Getenv("IM_INTERNAL_SECRET"), allowedOrigins: origins}
}

func (s *server) websocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, EnableCompression: false, CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		_, ok := s.config.allowedOrigins[origin]
		return ok
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.metrics.accepted.Add(1)
	c := &client{conn: conn, send: make(chan []byte, 256)}
	go s.writeLoop(c)
	s.readLoop(c)
}

func (s *server) readLoop(c *client) {
	defer s.remove(c)
	c.conn.SetReadLimit(maxFrameBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(authDeadline))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(pongWait)) })
	for {
		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			s.sendResponse(c, envelope{Code: 10003, Data: nil, Message: "Text JSON frames are required"})
			continue
		}
		var cmd command
		if err := json.Unmarshal(data, &cmd); err != nil || cmd.Cmd == "" {
			s.sendResponse(c, envelope{Code: 10003, Data: nil, Message: "Invalid command"})
			continue
		}
		if c.userUUID == "" {
			if cmd.Cmd != "auth" || len(cmd.Opts) != 1 {
				s.sendResponse(c, envelope{Code: 10002, Data: nil, Message: "Authentication required"})
				_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication required"), time.Now().Add(writeWait))
				return
			}
			token, ok := cmd.Opts[0].(string)
			if !ok || token == "" {
				s.authFailure(c)
				return
			}
			userUUID, err := s.authenticate(rContext(c), token)
			if err != nil || userUUID == "" {
				s.authFailure(c)
				return
			}
			c.userUUID = userUUID
			s.hub.add(c)
			s.hub.metrics.authenticated.Add(1)
			_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
			s.sendResponse(c, envelope{Code: 0, Data: "OK"})
			continue
		}
		send := envelope{Code: 10003, Data: nil, Message: "Unknown command"}
		if cmd.Cmd == "ping" {
			send = envelope{Code: 0, Data: "PONG"}
		}
		s.sendResponse(c, send)
	}
}

func (s *server) writeLoop(c *client) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case data, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok || c.conn.WriteMessage(websocket.TextMessage, data) != nil {
				return
			}
		case <-ticker.C:
			if c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)) != nil {
				return
			}
		}
	}
}

func (s *server) authenticate(ctx context.Context, token string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.config.backendURL+"/internal/v1/auth", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-IM-Internal-Secret", s.config.internalSecret)
	response, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var body struct {
		Data struct {
			UserUUID string `json:"user_uuid"`
		} `json:"data"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&body) != nil {
		return "", errors.New("invalid token")
	}
	return body.Data.UserUUID, nil
}

func (s *server) deliver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !secretOK(s.config.internalSecret, r.Header.Get("X-IM-Internal-Secret")) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var event struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFrameBytes)).Decode(&event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid event"})
		return
	}
	var routing struct {
		Targets struct {
			UserUUIDs []string `json:"user_uuids"`
		} `json:"targets"`
	}
	if json.Unmarshal(event.Payload, &routing) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	delivered := s.hub.deliver(routing.Targets.UserUUIDs, event.Payload)
	s.hub.metrics.delivered.Add(int64(delivered))
	writeJSON(w, http.StatusOK, map[string]int{"delivered": delivered})
}

// online reports the user uuids with at least one authenticated connection on
// this instance. It is used by the backend admin status; across multiple
// gateway instances the per-instance lists must be merged by the caller.
func (s *server) online(w http.ResponseWriter, r *http.Request) {
	if !secretOK(s.config.internalSecret, r.Header.Get("X-IM-Internal-Secret")) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.hub.mu.RLock()
	userUUIDs := make([]string, 0, len(s.hub.users))
	for userUUID := range s.hub.users {
		userUUIDs = append(userUUIDs, userUUID)
	}
	s.hub.mu.RUnlock()
	sort.Strings(userUUIDs)
	writeJSON(w, http.StatusOK, map[string]any{"user_uuids": userUUIDs})
}

// kick drops all local connections of a user (credential revocation). With
// multiple gateway instances the backend must call each instance; connections
// on unreachable instances end at their next reconnect, when the revoked
// credential no longer authenticates.
func (s *server) kick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !secretOK(s.config.internalSecret, r.Header.Get("X-IM-Internal-Secret")) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body struct {
		UserUUID string `json:"user_uuid"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFrameBytes)).Decode(&body); err != nil || body.UserUUID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"kicked": s.hub.kick(body.UserUUID)})
}

func (h *hub) add(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.users[c.userUUID] == nil {
		h.users[c.userUUID] = make(map[*client]struct{})
	}
	h.users[c.userUUID][c] = struct{}{}
}

// kick drops every connection of a user whose credentials were revoked. The
// read loops clean the hub entry up when the closed sockets error out.
func (h *hub) kick(userUUID string) int {
	h.mu.RLock()
	clients := make([]*client, 0, len(h.users[userUUID]))
	for c := range h.users[userUUID] {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	for _, c := range clients {
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "credentials revoked"), time.Now().Add(writeWait))
		_ = c.conn.Close()
	}
	return len(clients)
}

func (s *server) remove(c *client) {
	c.close.Do(func() {
		s.hub.mu.Lock()
		if connections := s.hub.users[c.userUUID]; connections != nil {
			delete(connections, c)
			if len(connections) == 0 {
				delete(s.hub.users, c.userUUID)
			}
		}
		s.hub.mu.Unlock()
		_ = c.conn.Close()
		s.hub.metrics.closed.Add(1)
	})
}

func (h *hub) deliver(userUUIDs []string, payload []byte) int {
	h.mu.RLock()
	clients := make(map[*client]struct{})
	for _, userUUID := range userUUIDs {
		for c := range h.users[userUUID] {
			clients[c] = struct{}{}
		}
	}
	h.mu.RUnlock()
	delivered := 0
	for c := range clients {
		select {
		case c.send <- payload:
			delivered++
		default:
			h.metrics.slowConsumers.Add(1)
			_ = c.conn.Close()
		}
	}
	return delivered
}

func (s *server) sendResponse(c *client, response envelope) {
	data, _ := json.Marshal(response)
	select {
	case c.send <- data:
	default:
		_ = c.conn.Close()
	}
}

func (s *server) authFailure(c *client) {
	s.hub.metrics.authFailed.Add(1)
	s.sendResponse(c, envelope{Code: 10001, Data: nil, Message: "Invalid bearer token"})
	time.Sleep(10 * time.Millisecond)
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid bearer token"), time.Now().Add(writeWait))
}

func (s *server) prometheus(w http.ResponseWriter, _ *http.Request) {
	h := s.hub
	h.mu.RLock()
	active := 0
	for _, clients := range h.users {
		active += len(clients)
	}
	onlineUsers := len(h.users)
	h.mu.RUnlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "airway_im_gateway_connections_active %d\nairway_im_gateway_users_online %d\nairway_im_gateway_connections_accepted_total %d\nairway_im_gateway_connections_authenticated_total %d\nairway_im_gateway_auth_failures_total %d\nairway_im_gateway_events_delivered_total %d\nairway_im_gateway_slow_consumers_total %d\n", active, onlineUsers, h.metrics.accepted.Load(), h.metrics.authenticated.Load(), h.metrics.authFailed.Load(), h.metrics.delivered.Load(), h.metrics.slowConsumers.Load())
}

func (s *server) dashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="5"><title>Airway IM Gateway</title><style>body{font:16px system-ui;margin:3rem;background:#111;color:#eee}pre{background:#222;padding:1rem;border-radius:8px}</style></head><body><h1>Airway IM Gateway</h1><p>Internal operational dashboard. Refreshes every 5 seconds.</p><pre>`)
	s.prometheus(w, nil)
	fmt.Fprint(w, "</pre></body></html>")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func secretOK(want, got string) bool {
	return want != "" && len(want) == len(got) && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func rContext(*client) context.Context { return context.Background() }
