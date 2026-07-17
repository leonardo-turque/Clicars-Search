package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zennitex/clicars-search/internal/domain"
	"github.com/zennitex/clicars-search/internal/whatsapp"
)

// fakeWAManager stubs the WhatsAppManager contract so the HTTP layer can be
// tested without whatsmeow or a database.
type fakeWAManager struct {
	sessionID     string
	qr            string
	connectErr    error
	sessions      []domain.WhatsAppSession
	listErr       error
	disconnectErr error
}

func (f *fakeWAManager) Connect(context.Context) (string, string, error) {
	return f.sessionID, f.qr, f.connectErr
}
func (f *fakeWAManager) List(context.Context) ([]domain.WhatsAppSession, error) {
	return f.sessions, f.listErr
}
func (f *fakeWAManager) Disconnect(context.Context, string) error { return f.disconnectErr }

func newWAServer(m WhatsAppManager) *httptest.Server {
	mux := http.NewServeMux()
	NewWhatsAppHandler(m).RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

func TestWhatsAppConnect_OK(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	const qr = "data:image/png;base64,AAAA"
	srv := newWAServer(&fakeWAManager{sessionID: id, qr: qr})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/whatsapp/connect")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var decoded connectResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.SessionID != id {
		t.Errorf("expected session_id %q, got %q", id, decoded.SessionID)
	}
	if decoded.QRCode != qr {
		t.Errorf("expected qr_code %q, got %q", qr, decoded.QRCode)
	}
}

func TestWhatsAppConnect_LimitReached(t *testing.T) {
	srv := newWAServer(&fakeWAManager{connectErr: whatsapp.ErrLimitReached})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/whatsapp/connect")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
}

func TestWhatsAppConnect_QRUnavailable(t *testing.T) {
	srv := newWAServer(&fakeWAManager{connectErr: whatsapp.ErrQRUnavailable})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/whatsapp/connect")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}
}

func TestWhatsAppList_OK(t *testing.T) {
	srv := newWAServer(&fakeWAManager{sessions: []domain.WhatsAppSession{
		{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", PhoneNumber: "5511999999999", Status: domain.WhatsAppConnected},
	}})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/whatsapp/sessions")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var decoded []domain.WhatsAppSession
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Status != domain.WhatsAppConnected {
		t.Errorf("unexpected sessions payload: %+v", decoded)
	}
}

func TestWhatsAppDelete_NoContent(t *testing.T) {
	srv := newWAServer(&fakeWAManager{})
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/whatsapp/sessions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestWhatsAppDelete_NotFound(t *testing.T) {
	srv := newWAServer(&fakeWAManager{disconnectErr: domain.ErrNotFound})
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/whatsapp/sessions/does-not-exist", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
