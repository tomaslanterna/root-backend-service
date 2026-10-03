package handlers

import (
	"context"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/lib/pq"
	"net/http"
	"net/url"
	"os"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"sync"
	"time"
)

type chatEvent struct {
	Type      string          `json:"type"`
	ChatID    string          `json:"chat_id,omitempty"`
	MessageID string          `json:"message_id,omitempty"`
	Message   *domain.Message `json:"message,omitempty"`
}

type chatConnection struct {
	socket   *websocket.Conn
	outgoing chan chatEvent
	done     chan struct{}
	once     sync.Once
}

func (c *chatConnection) close() { c.once.Do(func() { close(c.done); c.socket.Close() }) }
func (c *chatConnection) enqueue(event chatEvent) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.outgoing <- event:
	default:
		c.close()
	} // Reconnect resynchronizes a slow consumer.
}

// Every process listens to PostgreSQL; notifications are emitted only after commit.
// No participant IDs or JWTs are carried in the notification payload.
type ChatRealtime struct {
	service  ports.ChatService
	chats    ports.ChatRepository
	messages ports.MessageRepository
	listener *pq.Listener
	mu       sync.Mutex
	clients  map[string]map[*chatConnection]struct{}
	done     chan struct{}
	once     sync.Once
}

func NewChatRealtime(service ports.ChatService, chats ports.ChatRepository, messages ports.MessageRepository, connectionString string) (*ChatRealtime, error) {
	h := &ChatRealtime{service: service, chats: chats, messages: messages, clients: make(map[string]map[*chatConnection]struct{}), done: make(chan struct{})}
	h.listener = pq.NewListener(connectionString, time.Second, 30*time.Second, nil)
	if err := h.listener.Listen("root_chat_events"); err != nil {
		h.listener.Close()
		return nil, err
	}
	go h.listen()
	return h, nil
}
func (h *ChatRealtime) Close() {
	h.once.Do(func() {
		close(h.done)
		if h.listener != nil {
			h.listener.Close()
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, connections := range h.clients {
			for c := range connections {
				c.close()
			}
		}
	})
}
func (h *ChatRealtime) listen() {
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-h.done:
			return
		case notification := <-h.listener.Notify:
			if notification == nil {
				h.broadcastResync()
				continue
			}
			var event chatEvent
			if json.Unmarshal([]byte(notification.Extra), &event) == nil {
				h.dispatch(event)
			}
		case <-heartbeat.C:
			if err := h.listener.Ping(); err != nil {
				h.broadcastResync()
			}
		}
	}
}
func (h *ChatRealtime) broadcastResync() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, connections := range h.clients {
		for c := range connections {
			c.enqueue(chatEvent{Type: "resync"})
		}
	}
}
func (h *ChatRealtime) dispatch(event chatEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	chat, err := h.chats.GetChatByID(ctx, event.ChatID)
	cancel()
	if err != nil || chat == nil {
		return
	}
	h.mu.Lock()
	clients := make(map[string][]*chatConnection, len(chat.Participants))
	for _, participant := range chat.Participants {
		id := participant.ID
		connections := h.clients[id]
		for c := range connections {
			clients[id] = append(clients[id], c)
		}
	}
	h.mu.Unlock()
	for userID, connections := range clients {
		if event.Type == "chat.created" {
			for _, c := range connections {
				c.enqueue(event)
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// Membership was loaded above for this event, including group/revoked access.
		messages, err := h.messages.GetMessagesByChatID(ctx, event.ChatID, "id:"+event.MessageID, userID)
		cancel()
		if err != nil || len(messages) == 0 {
			continue
		}
		personalized := event
		personalized.Message = &messages[0]
		for _, c := range connections {
			c.enqueue(personalized)
		}
	}
}

func websocketOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host == r.Host {
		return true
	}
	for _, allowed := range strings.Split(os.Getenv("CHAT_ALLOWED_ORIGINS"), ",") {
		if origin == strings.TrimSpace(allowed) {
			return true
		}
	}
	// Development origins only; public deployments require an explicit allowlist.
	return origin == "http://localhost:3000" || origin == "http://127.0.0.1:3000"
}

func (h *ChatRealtime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		http.Error(w, "WebSocket does not accept query parameters", 400)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: websocketOriginAllowed, HandshakeTimeout: 5 * time.Second}
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &chatConnection{socket: socket, outgoing: make(chan chatEvent, 64), done: make(chan struct{})}
	defer c.close()
	socket.SetReadLimit(8192)
	socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := socket.ReadJSON(&auth); err != nil || auth.Type != "authenticate" {
		return
	}
	userID, err := parseToken(auth.Token)
	if err != nil {
		socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Authentication required"), time.Now().Add(time.Second))
		return
	}
	// parseToken verified signature; decode the same token's exp for connection lifetime.
	claims := jwt.MapClaims{}
	if _, _, err := new(jwt.Parser).ParseUnverified(auth.Token, claims); err != nil {
		return
	}
	expiry, err := claims.GetExpirationTime()
	if err != nil || expiry == nil {
		socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Invalid session expiry"), time.Now().Add(time.Second))
		return
	}
	lifetime := time.Until(expiry.Time)
	if lifetime <= 0 {
		return
	}
	// Check the authenticated subject's database access before registering.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	_, err = h.service.GetUserChats(ctx, userID)
	cancel()
	if err != nil {
		return
	}
	h.mu.Lock()
	select {
	case <-h.done:
		h.mu.Unlock()
		return
	default:
	}
	if h.clients[userID] == nil {
		h.clients[userID] = make(map[*chatConnection]struct{})
	}
	if len(h.clients[userID]) >= 8 {
		h.mu.Unlock()
		return
	}
	h.clients[userID][c] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients[userID], c)
		if len(h.clients[userID]) == 0 {
			delete(h.clients, userID)
		}
		h.mu.Unlock()
	}()
	socket.SetReadDeadline(time.Now().Add(70 * time.Second))
	socket.SetPongHandler(func(string) error { return socket.SetReadDeadline(time.Now().Add(70 * time.Second)) })
	go func() {
		tick := time.NewTicker(25 * time.Second)
		defer tick.Stop()
		expired := time.NewTimer(lifetime)
		defer expired.Stop()
		defer c.close()
		for {
			select {
			case <-c.done:
				return
			case <-expired.C:
				socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session expired"), time.Now().Add(time.Second))
				return
			case event := <-c.outgoing:
				socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if socket.WriteJSON(event) != nil {
					return
				}
			case <-tick.C:
				if socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					return
				}
			}
		}
	}()
	c.enqueue(chatEvent{Type: "ready"})
	for {
		// Application commands use authenticated HTTP. Only pong/control frames are expected.
		if _, _, err := socket.ReadMessage(); err != nil {
			return
		}
		socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4400, "Unexpected command"), time.Now().Add(time.Second))
		return
	}
}
