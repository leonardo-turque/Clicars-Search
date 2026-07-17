package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zennitex/clicars-search/internal/campaign"
	"github.com/zennitex/clicars-search/internal/domain"
)

// CampaignService is the contract the handler depends on, implemented by
// campaign.Dispatcher.
type CampaignService interface {
	StartCampaign(ctx context.Context, searchID, sessionID, message string) (*domain.Campaign, error)
	GetCampaign(ctx context.Context, id string) (*domain.Campaign, error)
	ListCampaigns(ctx context.Context, limit int) ([]domain.CampaignSummary, error)
}

// CampaignHandler exposes the messaging campaign engine over HTTP.
type CampaignHandler struct {
	svc CampaignService
}

func NewCampaignHandler(svc CampaignService) *CampaignHandler {
	return &CampaignHandler{svc: svc}
}

// RegisterRoutes wires the campaign routes onto the mux. Go 1.22 method-aware
// patterns give automatic 405s for the wrong verb.
func (h *CampaignHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/campaigns", h.handleList)
	mux.HandleFunc("POST /api/v1/campaigns", h.handleCreate)
	mux.HandleFunc("GET /api/v1/campaigns/{id}", h.handleGet)
}

type campaignRequest struct {
	SearchID  string `json:"search_id"`
	SessionID string `json:"whatsapp_session_id"`
	Message   string `json:"message"`
}

type campaignResponse struct {
	ID                string    `json:"id"`
	SearchID          string    `json:"search_id"`
	WhatsAppSessionID string    `json:"whatsapp_session_id"`
	Status            string    `json:"status"`
	Total             int       `json:"total"`
	Sent              int       `json:"sent"`
	Failed            int       `json:"failed"`
	Progress          string    `json:"progress"` // e.g. "15 de 100 enviados"
	CreatedAt         time.Time `json:"created_at"`
}

type campaignSummaryResponse struct {
	campaignResponse
	SearchNiche    string `json:"search_niche"`
	SearchLocation string `json:"search_location"`
	PhoneNumber    string `json:"phone_number"`
}

func toCampaignResponse(c *domain.Campaign) campaignResponse {
	return campaignResponse{
		ID:                c.ID,
		SearchID:          c.SearchID,
		WhatsAppSessionID: c.WhatsAppSessionID,
		Status:            c.Status,
		Total:             c.Total,
		Sent:              c.Sent,
		Failed:            c.Failed,
		Progress:          fmt.Sprintf("%d de %d enviados", c.Sent, c.Total),
		CreatedAt:         c.CreatedAt,
	}
}

// handleList returns the most recent 50 campaigns enriched with search and session metadata.
func (h *CampaignHandler) handleList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	summaries, err := h.svc.ListCampaigns(ctx, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "falha ao listar campanhas")
		return
	}

	out := make([]campaignSummaryResponse, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, campaignSummaryResponse{
			campaignResponse: toCampaignResponse(&s.Campaign),
			SearchNiche:      s.SearchNiche,
			SearchLocation:   s.SearchLocation,
			PhoneNumber:      s.PhoneNumber,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreate starts a campaign and returns it immediately (status PENDING);
// dispatch then proceeds in the background and is observed via handleGet.
func (h *CampaignHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req campaignRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	c, err := h.svc.StartCampaign(ctx, req.SearchID, req.SessionID, req.Message)
	if err != nil {
		switch {
		case errors.Is(err, campaign.ErrEmptyMessage):
			writeError(w, http.StatusBadRequest, "a mensagem é obrigatória")
		case errors.Is(err, campaign.ErrNoSession):
			writeError(w, http.StatusBadRequest, "selecione um número de WhatsApp")
		case errors.Is(err, campaign.ErrSessionNotReady):
			writeError(w, http.StatusConflict, "o número de WhatsApp selecionado não está conectado")
		case errors.Is(err, campaign.ErrNoLeads):
			writeError(w, http.StatusNotFound, "nenhum telefone encontrado para esta busca")
		default:
			writeError(w, http.StatusInternalServerError, "falha ao iniciar a campanha")
		}
		return
	}

	writeJSON(w, http.StatusCreated, toCampaignResponse(c))
}

// handleGet returns the live progress of a campaign.
func (h *CampaignHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	c, err := h.svc.GetCampaign(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "campanha não encontrada")
			return
		}
		writeError(w, http.StatusInternalServerError, "falha ao buscar a campanha")
		return
	}

	writeJSON(w, http.StatusOK, toCampaignResponse(c))
}
