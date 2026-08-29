package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zennitex/clicars-search/internal/protect"
)

type fakeProtect struct {
	snaps    []protect.Snapshot
	contacts []protect.Contact
	pauseErr error
	addErr   error
}

func (f *fakeProtect) Snapshots(context.Context) ([]protect.Snapshot, error) {
	return f.snaps, nil
}
func (f *fakeProtect) Pause(context.Context, string) error  { return f.pauseErr }
func (f *fakeProtect) Resume(context.Context, string) error { return f.pauseErr }
func (f *fakeProtect) ListContacts(context.Context) ([]protect.Contact, error) {
	return f.contacts, nil
}
func (f *fakeProtect) AddContact(_ context.Context, phone, label string) (*protect.Contact, error) {
	if f.addErr != nil {
		return nil, f.addErr
	}
	return &protect.Contact{ID: "c1", Phone: phone, Label: label, Active: true, CreatedAt: time.Now()}, nil
}
func (f *fakeProtect) DeleteContact(context.Context, string) error { return nil }

func newProtectServer(s ProtectService) *httptest.Server {
	mux := http.NewServeMux()
	NewProtectHandler(s).RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

func TestProtectList_OK(t *testing.T) {
	srv := newProtectServer(&fakeProtect{snaps: []protect.Snapshot{{
		Profile: protect.Profile{SessionID: "s1", PhoneNumber: "554184376916", Stage: protect.StageMature, DailyCap: 200, RemainingToday: 200},
		HourlyCap: 20,
	}}})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/protect/numbers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out []protect.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].DailyCap != 200 {
		t.Fatalf("unexpected payload: %+v", out)
	}
}

func TestProtectPause_NotFound(t *testing.T) {
	srv := newProtectServer(&fakeProtect{pauseErr: protect.ErrProfileNotFound})
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/v1/protect/numbers/missing/pause", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestProtectAddContact_Invalid(t *testing.T) {
	srv := newProtectServer(&fakeProtect{addErr: protect.ErrInvalidContact})
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/v1/protect/contacts", "application/json", strings.NewReader(`{"phone":"abc"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
