package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreateForumTopicReturnsTheThreadID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottok/createForumTopic" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body createForumTopicRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.ChatID != -1001234 || body.Name != "Login" {
			t.Errorf("request = %+v, want chat_id=-1001234 name=Login", body)
		}
		w.Write([]byte(`{"ok":true,"result":{"message_thread_id":42,"name":"Login"}}`))
	}))
	defer srv.Close()

	s := NewSender(&http.Client{Timeout: 5 * time.Second}, srv.URL)
	id, err := s.CreateForumTopic(context.Background(), Config{APIToken: "tok"}, -1001234, "Login")
	if err != nil {
		t.Fatalf("CreateForumTopic: %v", err)
	}
	if id != 42 {
		t.Errorf("thread id = %d, want 42", id)
	}
}

// TestCreateForumTopicSurfacesTelegramsOwnError covers the real failure
// mode an admin will hit first: a group that doesn't have Topics (forum
// mode) turned on yet. The caller needs that exact message, not a generic
// "request failed", to know what to fix.
func TestCreateForumTopicSurfacesTelegramsOwnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"description":"Bad Request: chat is not a forum"}`))
	}))
	defer srv.Close()

	s := NewSender(&http.Client{Timeout: 5 * time.Second}, srv.URL)
	_, err := s.CreateForumTopic(context.Background(), Config{APIToken: "tok"}, -1001234, "Login")
	if err == nil {
		t.Fatal("CreateForumTopic: want an error, got nil")
	}
	if got := err.Error(); got != "telegram: Bad Request: chat is not a forum" {
		t.Errorf("error = %q, want it to carry Telegram's own description", got)
	}
}
