package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
	"github.com/zennitex/clicars-search/internal/usecase"
)

// Searcher is the usecase contract the handler depends on.
type Searcher interface {
	Execute(ctx context.Context, in usecase.SearchInput) (*usecase.SearchResult, error)
}

// HistoryReader is the contract for fetching previously stored searches.
type HistoryReader interface {
	GetSearches(ctx context.Context, limit int) ([]domain.Search, error)
	GetSearchByID(ctx context.Context, id string) (*domain.Search, []domain.Company, error)
}

// SearchHandler exposes the search usecase over HTTP.
type SearchHandler struct {
	searcher      Searcher
	historyReader HistoryReader
}

func NewSearchHandler(s Searcher, h HistoryReader) *SearchHandler {
	return &SearchHandler{searcher: s, historyReader: h}
}

// RegisterRoutes wires the handler onto the mux.
func (h *SearchHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/searches", h.handleSearch)
	mux.HandleFunc("GET /api/v1/searches", h.handleListSearches)
	mux.HandleFunc("GET /api/v1/searches/{id}", h.handleGetSearch)
}

type searchRequest struct {
	Niche    string `json:"niche"`
	Location string `json:"location"`
	Quantity int    `json:"quantity"`
}

// searchStartedResponse is returned by POST — the job is accepted but not yet done.
type searchStartedResponse struct {
	SearchID string `json:"search_id"`
	Status   string `json:"status"`
}

// searchDetailResponse is returned by GET — includes live progress and companies.
type searchDetailResponse struct {
	SearchID                  string           `json:"search_id"`
	Status                    string           `json:"status"`
	Niche                     string           `json:"niche"`
	Location                  string           `json:"location"`
	Quantity                  int              `json:"quantity"`
	Progress                  int              `json:"progress"`
	ErrorMsg                  string           `json:"error_msg,omitempty"`
	StartedAt                 *time.Time       `json:"started_at,omitempty"`
	CompletedAt               *time.Time       `json:"completed_at,omitempty"`
	EstimatedSecondsRemaining *int             `json:"estimated_seconds_remaining,omitempty"`
	CreatedAt                 time.Time        `json:"created_at"`
	Companies                 []domain.Company `json:"companies"`
}

// handleSearch accepts a search request, starts the async job and returns 202.
func (h *SearchHandler) handleSearch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req searchRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	// Use a short timeout just for validation + DB record creation (the heavy
	// scraping runs in the background goroutine).
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := h.searcher.Execute(ctx, usecase.SearchInput{
		Niche:    req.Niche,
		Location: req.Location,
		Quantity: req.Quantity,
	})
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, searchStartedResponse{
		SearchID: result.SearchID,
		Status:   result.Status,
	})
}

func (h *SearchHandler) handleListSearches(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	searches, err := h.historyReader.GetSearches(ctx, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch search history")
		return
	}

	writeJSON(w, http.StatusOK, searches)
}

func (h *SearchHandler) handleGetSearch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	search, companies, err := h.historyReader.GetSearchByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "search not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch search")
		return
	}

	resp := searchDetailResponse{
		SearchID:    search.ID,
		Status:      search.Status,
		Niche:       search.Niche,
		Location:    search.Location,
		Quantity:    search.Quantity,
		Progress:    search.Progress,
		ErrorMsg:    search.ErrorMsg,
		StartedAt:   search.StartedAt,
		CompletedAt: search.CompletedAt,
		CreatedAt:   search.CreatedAt,
		Companies:   companies,
	}

	// Estimate remaining time while running: (elapsed / progress) * remaining.
	if search.Status == domain.SearchStatusRunning &&
		search.StartedAt != nil &&
		search.Progress > 0 &&
		search.Quantity > search.Progress {

		elapsed := time.Since(*search.StartedAt).Seconds()
		rate := float64(search.Progress) / elapsed
		est := int(float64(search.Quantity-search.Progress) / rate)
		resp.EstimatedSecondsRemaining = &est
	}

	writeJSON(w, http.StatusOK, resp)
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, usecase.ErrInvalidInput):
		return http.StatusBadRequest
	case errors.Is(err, usecase.ErrUpstream):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
