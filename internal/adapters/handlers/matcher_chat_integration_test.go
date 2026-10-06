package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"root-backend-service/internal/adapters/handlers"
	"root-backend-service/internal/adapters/repository/postgres"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/services"
)

func matcherTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
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
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	parsed, err := url.Parse(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = strings.Replace(parsed.Host, "-pooler.", ".", 1)
	return db, parsed.String()
}

// Fixtures are exclusively generated UUIDs. The test event is in the past and
// not featured, so it does not appear in the public upcoming-events listing.
func TestMatcherChatPostgresIntegration(t *testing.T) {
	db, listenerURL := matcherTestDB(t)
	ctx := context.Background()
	users := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	eventID, legacyID, unrelatedChatID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		rows, err := db.Query("SELECT chat_room_id FROM squads WHERE event_id=$1 AND chat_room_id IS NOT NULL", eventID)
		chats := []string{unrelatedChatID}
		if err != nil {
			t.Error(err)
		} else {
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Error(err)
				} else {
					chats = append(chats, id)
				}
			}
			if err := rows.Err(); err != nil {
				t.Error(err)
			}
			rows.Close()
		}
		statements := []struct {
			sql string
			arg interface{}
		}{
			{"DELETE FROM message_receipts WHERE message_id IN(SELECT id FROM messages WHERE chat_id=ANY($1::uuid[]))", pq.Array(chats)},
			{"DELETE FROM messages WHERE chat_id=ANY($1::uuid[])", pq.Array(chats)},
			{"DELETE FROM chat_participants WHERE chat_id=ANY($1::uuid[])", pq.Array(chats)},
			{"DELETE FROM squad_members WHERE squad_id IN(SELECT id FROM squads WHERE event_id=$1)", eventID},
			{"DELETE FROM squads WHERE event_id=$1", eventID},
			{"DELETE FROM chats WHERE id=ANY($1::uuid[])", pq.Array(chats)},
			{"DELETE FROM event_swipes WHERE event_id=$1", eventID},
			{"DELETE FROM events WHERE id=$1", eventID},
			{"DELETE FROM users WHERE id=ANY($1::uuid[])", pq.Array(users)},
		}
		for _, statement := range statements {
			if _, err := db.Exec(statement.sql, statement.arg); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	userRepo := postgres.NewUserRepository(db)
	for _, id := range users {
		now := time.Now().UTC()
		name := "matcher_test_" + strings.ReplaceAll(id, "-", "")
		if err := userRepo.CreateUser(ctx, &domain.User{ID: id, Email: name + "@example.invalid", Username: name, Name: "Matcher integration", Role: "USER", PasswordHash: "login-disabled", Followers: []string{}, Following: []string{}, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO events(id,title,date,location,cinematic_banner_url,description,lineup,is_featured) VALUES($1,'Isolated matcher integration fixture','2000-01-01','Test only','','',ARRAY[]::text[],false)", eventID); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewMatchRepository(db)
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	service := services.NewMatchService(repo)
	chatRepo := postgres.NewChatRepository(db)
	messageRepo := postgres.NewMessageRepository(db)
	if err := messageRepo.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	chatService := services.NewChatService(chatRepo, messageRepo)
	realtime, err := handlers.NewChatRealtime(chatService, chatRepo, messageRepo, listenerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_SECRET", "matcher-isolated-test-secret")
	router := chi.NewRouter()
	mh, ch := handlers.NewMatchHandler(service), handlers.NewChatHandler(chatService)
	router.Get("/v1/chats/ws", realtime.ServeHTTP)
	router.Group(func(r chi.Router) {
		r.Use(handlers.AuthMiddleware)
		r.Post("/v1/events/{eventId}/swipes", mh.HandleSwipe)
		r.Get("/v1/crews/matches", mh.GetMatches)
		r.Post("/v1/crews/{id}/chat", mh.EnsureSquadChat)
		r.Get("/v1/chats", ch.GetUserChats)
		r.Get("/v1/chats/{id}", ch.GetChatByID)
		r.Get("/v1/chats/{id}/messages", ch.GetMessages)
		r.Post("/v1/chats/{id}/messages", ch.SendMessage)
		r.Post("/v1/chats/{id}/receipts", ch.AcknowledgeMessages)
	})
	server := httptest.NewServer(router)
	t.Cleanup(func() { realtime.Close(); server.Close() })
	token := func(user string) string {
		value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": user, "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("matcher-isolated-test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	request := func(user, method, path string, body interface{}, status int) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if user != "" {
			req.Header.Set("Authorization", "Bearer "+token(user))
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result json.RawMessage
		if res.StatusCode != 204 {
			json.NewDecoder(res.Body).Decode(&result)
		}
		if res.StatusCode != status {
			t.Fatalf("%s %s => %d want %d body=%s", method, path, res.StatusCode, status, result)
		}
		return result
	}
	type event struct {
		Type    string          `json:"type"`
		ChatID  string          `json:"chat_id"`
		Message *domain.Message `json:"message"`
	}
	sockets := make([]*websocket.Conn, 0)
	channels := make([]<-chan event, 0)
	for _, user := range users {
		socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/chats/ws", http.Header{"Origin": []string{"http://localhost:3000"}})
		if err != nil {
			t.Fatal(err)
		}
		sockets = append(sockets, socket)
		t.Cleanup(func() { socket.Close() })
		if err := socket.WriteJSON(map[string]string{"type": "authenticate", "token": token(user)}); err != nil {
			t.Fatal(err)
		}
		socket.SetReadDeadline(time.Now().Add(10 * time.Second))
		var ready event
		if err := socket.ReadJSON(&ready); err != nil || ready.Type != "ready" {
			t.Fatalf("ready: %v %#v", err, ready)
		}
		socket.SetReadDeadline(time.Time{})
		events := make(chan event, 50)
		channels = append(channels, events)
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
	}
	await := func(stream <-chan event, kind, chatID string, status domain.MessageStatus) {
		t.Helper()
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		for {
			select {
			case received, ok := <-stream:
				if !ok {
					t.Fatal("socket closed")
				}
				if received.Type == kind && received.ChatID == chatID && (status == "" || (received.Message != nil && received.Message.Status == status)) {
					return
				}
			case <-timer.C:
				t.Fatalf("missing %s status %s", kind, status)
			}
		}
	}
	request("", http.MethodGet, "/v1/crews/matches", nil, 401)
	request(users[0], http.MethodPost, "/v1/crews/sq1/chat", nil, 400)
	request(users[0], http.MethodPost, "/v1/crews/"+uuid.NewString()+"/chat", nil, 404)
	request(users[0], http.MethodPost, "/v1/events/"+eventID+"/swipes", map[string]interface{}{"direction": "invalid"}, 400)
	first := request(users[0], http.MethodPost, "/v1/events/"+eventID+"/swipes", map[string]interface{}{"direction": "like"}, 200)
	if !bytes.Contains(first, []byte("queued")) {
		t.Fatalf("first swipe: %s", first)
	}
	var result struct {
		Status string              `json:"status"`
		Crew   domain.MatcherSquad `json:"crew"`
	}
	raw := request(users[1], http.MethodPost, "/v1/events/"+eventID+"/swipes", map[string]interface{}{"direction": "like", "user_id": users[2], "preferences": map[string]string{"userId": users[2]}}, 200)
	if err := json.Unmarshal(raw, &result); err != nil || result.Status != "matched" || len(result.Crew.Members) != 2 || result.Crew.ChatRoomID == "" {
		t.Fatalf("real squad/chat DTO: %s %v", raw, err)
	}
	squadID, chatID := result.Crew.ID, result.Crew.ChatRoomID
	for _, stream := range channels[:2] {
		await(stream, "chat.created", chatID, "")
	}
	for _, user := range users[:2] {
		list := request(user, http.MethodGet, "/v1/crews/matches", nil, 200)
		if !bytes.Contains(list, []byte(squadID)) || !bytes.Contains(list, []byte(chatID)) {
			t.Fatalf("missing squad after reload: %s", list)
		}
		chats := request(user, http.MethodGet, "/v1/chats", nil, 200)
		if !bytes.Contains(chats, []byte(chatID)) {
			t.Fatalf("missing conversation: %s", chats)
		}
	}
	outsider := request(users[2], http.MethodGet, "/v1/crews/matches", nil, 200)
	if bytes.Contains(outsider, []byte(squadID)) {
		t.Fatal("leaked squad")
	}
	request(users[2], http.MethodPost, "/v1/crews/"+squadID+"/chat", nil, 403)
	request(users[2], http.MethodGet, "/v1/chats/"+chatID+"/messages", nil, 403)
	request(users[2], http.MethodPost, "/v1/chats/"+chatID+"/messages", map[string]string{"content": "forbidden"}, 403)
	id := uuid.NewString()
	message := map[string]string{"content": "Persisted squad message", "type": "text", "client_message_id": id}
	request(users[0], http.MethodPost, "/v1/chats/"+chatID+"/messages", message, 201)
	await(channels[1], "message.created", chatID, domain.MessageStatusSent)
	request(users[1], http.MethodPost, "/v1/chats/"+chatID+"/receipts", map[string]interface{}{"message_ids": []string{id}, "read": true}, 204)
	await(channels[0], "message.updated", chatID, domain.MessageStatusRead)
	request(users[0], http.MethodPost, "/v1/chats/"+chatID+"/messages", message, 201)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE chat_id=$1", chatID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate messages: %d %v", count, err)
	}
	// Several concurrent opens/repeated swipes must reuse the exact same chat.
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := repo.EnsureSquadChat(ctx, squadID, users[0])
			if err != nil {
				failures <- err
			} else if got != chatID {
				failures <- fmt.Errorf("new duplicate chat %s", got)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := service.ProcessSwipe(ctx, &domain.EventSwipe{EventID: eventID, UserID: users[1], Direction: "like"})
			if err != nil {
				failures <- err
			} else if got["crew"].(*domain.MatcherSquad).ID != squadID {
				failures <- fmt.Errorf("duplicate squad")
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM squads WHERE event_id=$1", eventID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate squads: %d %v", count, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM chat_participants WHERE chat_id=$1", chatID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("duplicate participants: %d %v", count, err)
	}
	// A historic real squad with no chat is repaired on first authorized open.
	if _, err := db.Exec("INSERT INTO squads(id,event_id,name,status,type,expires_at) VALUES($1,$2,'Historic fixture','forming','event_match',NOW()+INTERVAL '1 day')", legacyID, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO squad_members(squad_id,user_id) VALUES($1,$2),($1,$3)", legacyID, users[0], users[1]); err != nil {
		t.Fatal(err)
	}
	raw = request(users[0], http.MethodPost, "/v1/crews/"+legacyID+"/chat", nil, 200)
	var historic struct {
		ChatID string `json:"chatId"`
	}
	if err := json.Unmarshal(raw, &historic); err != nil || historic.ChatID == "" {
		t.Fatal("missing historic chat")
	}
	raw2 := request(users[1], http.MethodPost, "/v1/crews/"+legacyID+"/chat", nil, 200)
	if !bytes.Equal(raw, raw2) {
		t.Fatal("historic repair created duplicates")
	}
	// A corrupted link to an unrelated conversation must fail closed.
	if _, err := db.Exec("INSERT INTO chats(id,type,last_message) VALUES($1,'DIRECT','')", unrelatedChatID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE squads SET chat_room_id=$2 WHERE id=$1", legacyID, unrelatedChatID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("UPDATE squads SET chat_room_id=$2 WHERE id=$1", legacyID, historic.ChatID); err != nil {
			t.Error(err)
		}
	})
	request(users[0], http.MethodPost, "/v1/crews/"+legacyID+"/chat", nil, 409)
	if err := db.QueryRow("SELECT COUNT(*) FROM chat_participants WHERE chat_id=$1", unrelatedChatID).Scan(&count); err != nil || count != 0 {
		t.Fatal("adopted unrelated conversation")
	}
	select {
	case event := <-channels[2]:
		t.Fatalf("outsider event: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
	t.Log("real matcher -> atomic squad/chat -> WebSocket, read without reply, persisted history, concurrent idempotency, historical repair and outsider restrictions passed")
}
