package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
)

func TestMapStatus(t *testing.T) {
	cases := map[string]string{
		"connected":    domain.WhatsAppConnected,
		"CONNECTED":    domain.WhatsAppConnected,
		"connecting":   domain.WhatsAppConnecting,
		"qr_pending":   domain.WhatsAppConnecting,
		"disconnected": domain.WhatsAppDisconnected,
		"logged_out":   domain.WhatsAppDisconnected,
		"":             domain.WhatsAppDisconnected,
	}
	for in, want := range cases {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDigitsOnly(t *testing.T) {
	if got := digitsOnly("+55 (11) 99999-8888"); got != "5511999998888" {
		t.Errorf("got %q", got)
	}
	if got := digitsOnly("5511999998888@s.whatsapp.net"); got != "5511999998888" {
		t.Errorf("got %q", got)
	}
}

func TestManagerListAndSend(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /instances", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Key") != "secret" {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode([]remoteInstance{{
			ID:          "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			Name:        "Clicars Search #1",
			Token:       "tok-1",
			PhoneNumber: "5511999998888",
			DeviceJID:   "5511999998888:1@s.whatsapp.net",
			Status:      "connected",
			CreatedAt:   time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		}})
	})
	mux.HandleFunc("POST /messages/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var body sendRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.To != "5511888777666" || body.Type != "text" || body.Text == "" {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(sendResponse{MessageID: "mid-1", Status: "sent"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m, err := NewManager(srv.URL, "secret", nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	sessions, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Status != domain.WhatsAppConnected {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
	if sessions[0].PhoneNumber != "5511999998888" {
		t.Errorf("phone=%q", sessions[0].PhoneNumber)
	}

	jid, ok, err := m.IsRegistered(context.Background(), sessions[0].ID, "+55 11 88877-7666")
	if err != nil || !ok || jid != "5511888777666" {
		t.Fatalf("IsRegistered: jid=%q ok=%v err=%v", jid, ok, err)
	}
	if err := m.SendText(context.Background(), sessions[0].ID, jid, "olá"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
}

func TestManagerConnectLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /instances", func(w http.ResponseWriter, r *http.Request) {
		list := make([]remoteInstance, MaxSessions)
		for i := range list {
			list[i] = remoteInstance{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i), Status: "disconnected"}
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m, err := NewManager(srv.URL, "secret", nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	_, _, err = m.Connect(context.Background())
	if !errors.Is(err, ErrLimitReached) {
		t.Fatalf("expected ErrLimitReached, got %v", err)
	}
}

func TestNewManagerRequiresConfig(t *testing.T) {
	if _, err := NewManager("", "key", nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
	if _, err := NewManager("https://example.com/api", "", nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
}
