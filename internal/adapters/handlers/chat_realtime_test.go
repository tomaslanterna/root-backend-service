package handlers

import (
	"context"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
	"strings"
	"testing"
	"time"
)

type realtimeTestService struct{ ports.ChatService }

func (realtimeTestService) GetUserChats(context.Context, string) ([]domain.Chat, error) {
	return nil, nil
}
func realtimeTestToken(t *testing.T, expiry time.Time) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "test-user", "exp": expiry.Unix()}).SignedString([]byte("test-websocket-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func TestWebsocketOriginAllowlist(t *testing.T) {
	t.Setenv("CHAT_ALLOWED_ORIGINS", "https://root.example,capacitor://localhost")
	for _, test := range []struct {
		origin  string
		allowed bool
	}{
		{"http://localhost:3000", true}, {"https://root.example", true}, {"capacitor://localhost", true},
		{"https://root.example.attacker.test", false}, {"null", false}, {"", false},
	} {
		request := httptest.NewRequest("GET", "http://backend.example/v1/chats/ws", nil)
		request.Header.Set("Origin", test.origin)
		if actual := websocketOriginAllowed(request); actual != test.allowed {
			t.Fatalf("origin %q allowed=%v", test.origin, actual)
		}
	}
}
func TestWebsocketAuthenticationAndExpiry(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-websocket-secret")
	h := &ChatRealtime{service: realtimeTestService{}, clients: make(map[string]map[*chatConnection]struct{}), done: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(h.ServeHTTP))
	defer server.Close()
	defer h.Close()
	connect := func(token string) *websocket.Conn {
		t.Helper()
		socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Origin": []string{"http://localhost:3000"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := socket.WriteJSON(map[string]string{"type": "authenticate", "token": token}); err != nil {
			t.Fatal(err)
		}
		socket.SetReadDeadline(time.Now().Add(5 * time.Second))
		return socket
	}
	invalid := connect("invalid-token")
	defer invalid.Close()
	if _, _, err := invalid.ReadMessage(); !websocket.IsCloseError(err, 4401) {
		t.Fatalf("invalid token: %v", err)
	}
	socket := connect(realtimeTestToken(t, time.Now().Add(2*time.Second)))
	defer socket.Close()
	var event chatEvent
	if err := socket.ReadJSON(&event); err != nil || event.Type != "ready" {
		t.Fatalf("expected ready: %#v %v", event, err)
	}
	if _, _, err := socket.ReadMessage(); !websocket.IsCloseError(err, 4401) {
		t.Fatalf("expected expired session: %v", err)
	}
	request := httptest.NewRequest("GET", "http://backend.example/v1/chats/ws?token=must-not-be-accepted", nil)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != 400 {
		t.Fatalf("URL credentials allowed: %d", recorder.Code)
	}
}

func TestSlowWebsocketConsumerIsDisconnected(t *testing.T) {
	// A real connection avoids mocking the underlying transport's Close method.
	upgraded := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			upgraded <- conn
		}
	}))
	defer server.Close()
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	c := &chatConnection{socket: <-upgraded, outgoing: make(chan chatEvent, 1), done: make(chan struct{})}
	c.enqueue(chatEvent{Type: "ready"})
	c.enqueue(chatEvent{Type: "resync"})
	select {
	case <-c.done:
	default:
		t.Fatal("slow consumer was not closed")
	}
}
