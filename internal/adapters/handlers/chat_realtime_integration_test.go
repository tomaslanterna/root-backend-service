package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"root-backend-service/internal/adapters/handlers"
	"root-backend-service/internal/adapters/repository/postgres"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/services"
	"strings"
	"testing"
	"time"
)

// Opt-in integration uses temporary, isolated users/conversations and deletes only
// their UUIDs on completion. No existing conversations or messages are changed.
func TestChatRealtimePostgresIntegration(t *testing.T) {
	dbURL := os.Getenv("CHAT_INTEGRATION_DATABASE_URL")
	if dbURL == "" && os.Getenv("CHAT_INTEGRATION_USE_LOCAL_ENV") == "1" {
		values, err := godotenv.Read("../../../.env")
		if err != nil {
			t.Fatal(err)
		}
		dbURL = values["DATABASE_URL"]
	}
	if dbURL == "" {
		t.Skip("set CHAT_INTEGRATION_DATABASE_URL or CHAT_INTEGRATION_USE_LOCAL_ENV=1")
	}
	listenerURL := dbURL
	if parsed, err := url.Parse(listenerURL); err == nil {
		parsed.Host = strings.Replace(parsed.Host, "-pooler.", ".", 1)
		listenerURL = parsed.String()
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	users := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	chatID := uuid.NewString()
	t.Cleanup(func() {
		statements := []string{
			`DELETE FROM message_receipts WHERE message_id IN(SELECT id FROM messages WHERE chat_id=$1)`,
			`DELETE FROM messages WHERE chat_id=$1`, `DELETE FROM chat_participants WHERE chat_id=$1`, `DELETE FROM chats WHERE id=$1`,
		}
		for _, statement := range statements {
			if _, err := db.Exec(statement, chatID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
		if _, err := db.Exec(`DELETE FROM users WHERE id=ANY($1::uuid[])`, pq.Array(users)); err != nil {
			t.Errorf("user fixture cleanup: %v", err)
		}
	})
	userRepo := postgres.NewUserRepository(db)
	for i, id := range users {
		now := time.Now().UTC()
		name := "ws_test_" + strings.ReplaceAll(id, "-", "")
		user := &domain.User{ID: id, Email: name + "@example.invalid", Username: name, Name: "WebSocket integration", Role: "USER", PasswordHash: "login-disabled", Followers: []string{}, Following: []string{}, CreatedAt: now, UpdatedAt: now}
		if err := userRepo.CreateUser(ctx, user); err != nil {
			t.Fatalf("creating fixture user %d: %v", i, err)
		}
	}
	chatRepo := postgres.NewChatRepository(db)
	messageRepo := postgres.NewMessageRepository(db)
	if err := messageRepo.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := chatRepo.CreateChat(ctx, &domain.Chat{ID: chatID, Type: domain.ChatTypeCrews, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range users[:2] {
		if err := chatRepo.AddParticipant(ctx, &domain.ChatParticipant{ChatID: chatID, UserID: id, JoinedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	service := services.NewChatService(chatRepo, messageRepo)
	t.Setenv("JWT_SECRET", "isolated-integration-secret")
	startServer := func() *httptest.Server {
		t.Helper()
		realtime, err := handlers.NewChatRealtime(service, chatRepo, messageRepo, listenerURL)
		if err != nil {
			t.Fatal(err)
		}
		router := chi.NewRouter()
		handler := handlers.NewChatHandler(service)
		router.Get("/v1/chats/ws", realtime.ServeHTTP)
		router.Group(func(r chi.Router) {
			r.Use(handlers.AuthMiddleware)
			r.Get("/v1/chats", handler.GetUserChats)
			r.Get("/v1/chats/{id}/messages", handler.GetMessages)
			r.Post("/v1/chats/{id}/messages", handler.SendMessage)
			r.Post("/v1/chats/{id}/receipts", handler.AcknowledgeMessages)
		})
		server := httptest.NewServer(router)
		t.Cleanup(func() { realtime.Close(); server.Close() })
		return server
	}
	serverA, serverB := startServer(), startServer()
	token := func(id string) string {
		value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": id, "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("isolated-integration-secret"))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	type event struct {
		Type    string          `json:"type"`
		Message *domain.Message `json:"message"`
	}
	connect := func(server *httptest.Server, id string) (*websocket.Conn, <-chan event) {
		t.Helper()
		socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/chats/ws", http.Header{"Origin": []string{"http://localhost:3000"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { socket.Close() })
		if err := socket.WriteJSON(map[string]string{"type": "authenticate", "token": token(id)}); err != nil {
			t.Fatal(err)
		}
		socket.SetReadDeadline(time.Now().Add(15 * time.Second))
		var ready event
		if err := socket.ReadJSON(&ready); err != nil || ready.Type != "ready" {
			t.Fatalf("handshake: %#v %v", ready, err)
		}
		socket.SetReadDeadline(time.Time{})
		events := make(chan event, 512)
		go func() {
			defer close(events)
			for {
				var received event
				if socket.ReadJSON(&received) != nil {
					return
				}
				events <- received
			}
		}()
		return socket, events
	}
	_, senderEvents := connect(serverA, users[0])
	_, secondTabEvents := connect(serverA, users[0])
	receiver, receiverEvents := connect(serverB, users[1])
	_, outsiderEvents := connect(serverA, users[2])
	request := func(server *httptest.Server, user, method, path string, body interface{}, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if user != "" {
			req.Header.Set("Authorization", "Bearer "+token(user))
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result json.RawMessage
		if response.StatusCode != 204 {
			json.NewDecoder(response.Body).Decode(&result)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, want, result)
		}
		return result
	}
	await := func(events <-chan event, id string, status domain.MessageStatus) domain.Message {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case received, ok := <-events:
				if !ok {
					t.Fatal("socket closed")
				}
				if received.Message != nil && received.Message.ID == id && received.Message.Status == status {
					return *received.Message
				}
			case <-deadline.C:
				t.Fatalf("missing websocket message %s status %s", id, status)
			}
		}
	}
	path := "/v1/chats/" + chatID
	request(serverA, "", http.MethodGet, path+"/messages", nil, 401)
	request(serverA, users[2], http.MethodGet, path+"/messages", nil, 403)
	request(serverA, users[2], http.MethodPost, path+"/messages", map[string]string{"content": "forbidden", "type": "text"}, 403)
	request(serverA, users[0], http.MethodGet, path+"/messages?before=invalid", nil, 400)
	id := uuid.NewString()
	payload := map[string]string{"content": "WebSocket integration message", "type": "text", "client_message_id": id}
	request(serverA, users[0], http.MethodPost, path+"/messages", payload, 201)
	await(senderEvents, id, domain.MessageStatusSent)
	await(secondTabEvents, id, domain.MessageStatusSent)
	await(receiverEvents, id, domain.MessageStatusSent)
	request(serverA, users[0], http.MethodPost, path+"/messages", payload, 201)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id=$1`, chatID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate message count=%d err=%v", count, err)
	}
	request(serverB, users[1], http.MethodGet, path+"/messages", nil, 200)
	if err := db.QueryRow(`SELECT COUNT(*) FROM message_receipts WHERE message_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("history fabricated receipt: %d %v", count, err)
	}
	receipt := map[string]interface{}{"message_ids": []string{id}, "read": false}
	request(serverB, users[2], http.MethodPost, path+"/receipts", receipt, 403)
	request(serverB, users[1], http.MethodPost, path+"/receipts", receipt, 204)
	await(senderEvents, id, domain.MessageStatusDelivered)
	receipt["read"] = true
	request(serverB, users[1], http.MethodPost, path+"/receipts", receipt, 204)
	await(senderEvents, id, domain.MessageStatusRead)
	await(secondTabEvents, id, domain.MessageStatusRead)
	chats := request(serverB, users[1], http.MethodGet, "/v1/chats", nil, 200)
	var list []domain.Chat
	if err := json.Unmarshal(chats, &list); err != nil || len(list) != 1 || list[0].UnreadCount != 0 {
		t.Fatalf("unread count: %s %v", chats, err)
	}
	select {
	case received := <-outsiderEvents:
		t.Fatalf("outsider received event: %#v", received)
	case <-time.After(200 * time.Millisecond):
	}
	// Disconnect the receiver, send another message, and recover through durable history.
	receiver.Close()
	offlineID := uuid.NewString()
	request(serverA, users[0], http.MethodPost, path+"/messages", map[string]string{"content": "offline recovery", "type": "text", "client_message_id": offlineID}, 201)
	_, reconnected := connect(serverB, users[1])
	_ = reconnected
	history := request(serverB, users[1], http.MethodGet, path+"/messages", nil, 200)
	var messages []domain.Message
	if err := json.Unmarshal(history, &messages); err != nil || len(messages) != 2 || messages[1].ID != offlineID {
		t.Fatalf("offline recovery: %s %v", history, err)
	}
	// Stable pagination, including equal timestamps, with more than one page offline.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC()
	for i := 0; i < 55; i++ {
		if _, err := tx.Exec(`INSERT INTO messages(id,chat_id,sender_id,content,type,timestamp) VALUES($1,$2,$3,'pagination fixture','text',$4)`, uuid.NewString(), chatID, users[0], timestamp); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	raw := request(serverB, users[1], http.MethodGet, path+"/messages", nil, 200)
	if err := json.Unmarshal(raw, &messages); err != nil || len(messages) != 50 {
		t.Fatalf("latest page: %d %v", len(messages), err)
	}
	cursor := messages[0].Timestamp.Format(time.RFC3339Nano) + "|" + messages[0].ID
	olderRaw := request(serverB, users[1], http.MethodGet, path+"/messages?before="+url.QueryEscape(cursor), nil, 200)
	var older []domain.Message
	if err := json.Unmarshal(olderRaw, &older); err != nil || len(older) != 7 {
		t.Fatalf("older page: %d %v", len(older), err)
	}
	seen := map[string]bool{}
	for _, message := range append(older, messages...) {
		if seen[message.ID] {
			t.Fatal("pagination duplicated ID")
		}
		seen[message.ID] = true
	}
	t.Log("real PostgreSQL + two server instances: persisted messages, multiple tabs, delivery/read without reply, authorization, idempotency, offline recovery and 57-message pagination passed")
}
