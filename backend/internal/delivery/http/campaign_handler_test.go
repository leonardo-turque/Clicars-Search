package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zennitex/clicars-search/internal/campaign"
	"github.com/zennitex/clicars-search/internal/domain"
)

// fakeCampaignService stubs the CampaignService contract so the HTTP layer can be
// tested without the dispatcher, whatsmeow or a database.
type fakeCampaignService struct {
	campaign *domain.Campaign
	startErr error
	getErr   error
}

func (f *fakeCampaignService) StartCampaign(context.Context, string, string, string) (*domain.Campaign, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.campaign, nil
}

func (f *fakeCampaignService) GetCampaign(context.Context, string) (*domain.Campaign, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.campaign, nil
}

func (f *fakeCampaignService) ListCampaigns(context.Context, int) ([]domain.CampaignSummary, error) {
	if f.campaign == nil {
		return nil, nil
	}
	return []domain.CampaignSummary{{Campaign: *f.campaign}}, nil
}

func newCampaignServer(s CampaignService) *httptest.Server {
	mux := http.NewServeMux()
	NewCampaignHandler(s).RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

func postCampaign(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url+"/api/v1/campaigns", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func TestCampaignCreate_Created(t *testing.T) {
	c := &domain.Campaign{
		ID: "11111111-1111-1111-1111-111111111111", SearchID: "s1",
		WhatsAppSessionID: "w1", Status: domain.CampaignPending, Total: 10,
		CreatedAt: time.Now(),
	}
	srv := newCampaignServer(&fakeCampaignService{campaign: c})
	defer srv.Close()

	resp := postCampaign(t, srv.URL, `{"search_id":"s1","whatsapp_session_id":"w1","message":"Oi"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var decoded campaignResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.ID != c.ID || decoded.Status != domain.CampaignPending {
		t.Errorf("unexpected payload: %+v", decoded)
	}
	if decoded.Progress != "0 de 10 enviados" {
		t.Errorf("unexpected progress string: %q", decoded.Progress)
	}
}

func TestCampaignCreate_ErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		startErr error
		want     int
	}{
		{"empty message", campaign.ErrEmptyMessage, http.StatusBadRequest},
		{"no session", campaign.ErrNoSession, http.StatusBadRequest},
		{"session not ready", campaign.ErrSessionNotReady, http.StatusConflict},
		{"no leads", campaign.ErrNoLeads, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newCampaignServer(&fakeCampaignService{startErr: tc.startErr})
			defer srv.Close()

			resp := postCampaign(t, srv.URL, `{"search_id":"s1","whatsapp_session_id":"w1","message":"Oi"}`)
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("expected %d, got %d", tc.want, resp.StatusCode)
			}
		})
	}
}

func TestCampaignCreate_BadJSON(t *testing.T) {
	srv := newCampaignServer(&fakeCampaignService{})
	defer srv.Close()

	resp := postCampaign(t, srv.URL, `{"search_id": }`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestCampaignGet_OK(t *testing.T) {
	c := &domain.Campaign{
		ID: "11111111-1111-1111-1111-111111111111", Status: domain.CampaignRunning,
		Total: 100, Sent: 15, Failed: 2,
	}
	srv := newCampaignServer(&fakeCampaignService{campaign: c})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/campaigns/" + c.ID)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var decoded campaignResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Progress != "15 de 100 enviados" {
		t.Errorf("unexpected progress: %q", decoded.Progress)
	}
}

func TestCampaignGet_NotFound(t *testing.T) {
	srv := newCampaignServer(&fakeCampaignService{getErr: domain.ErrNotFound})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/campaigns/does-not-exist")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
