package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
	"github.com/zennitex/clicars-search/internal/whatsapp"
)

// WhatsAppManager is the contract the handler depends on, implemented by
// whatsapp.Manager.
type WhatsAppManager interface {
	Connect(ctx context.Context) (sessionID string, qrBase64 string, err error)
	List(ctx context.Context) ([]domain.WhatsAppSession, error)
	Disconnect(ctx context.Context, id string) error
}

// WhatsAppHandler exposes the WhatsApp session manager over HTTP.
type WhatsAppHandler struct {
	manager WhatsAppManager
}

func NewWhatsAppHandler(m WhatsAppManager) *WhatsAppHandler {
	return &WhatsAppHandler{manager: m}
}

// RegisterRoutes wires the WhatsApp routes onto the mux. Go 1.22 method-aware
// patterns give automatic 405s for the wrong verb.
func (h *WhatsAppHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/whatsapp/connect", h.handleConnect)
	mux.HandleFunc("GET /api/v1/whatsapp/sessions", h.handleList)
	mux.HandleFunc("DELETE /api/v1/whatsapp/sessions/{id}", h.handleDelete)
}

type connectResponse struct {
	SessionID string `json:"session_id"`
	QRCode    string `json:"qr_code"` // data:image/png;base64,... ready for <img src>
}

// handleConnect starts a pairing and returns the QR code. The pairing then
// completes asynchronously; the client polls GET /sessions to see it go CONNECTED.
func (h *WhatsAppHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Generous timeout: whatsmeow's first QR can take a couple of seconds.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	id, qr, err := h.manager.Connect(ctx)
	if err != nil {
		switch {
		case errors.Is(err, whatsapp.ErrLimitReached):
			writeError(w, http.StatusConflict, "limite de 15 números atingido")
		case errors.Is(err, whatsapp.ErrQRUnavailable):
			writeError(w, http.StatusBadGateway, "não foi possível gerar o QR Code, tente novamente")
		default:
			writeError(w, http.StatusInternalServerError, "erro ao iniciar a conexão")
		}
		return
	}

	writeJSON(w, http.StatusOK, connectResponse{SessionID: id, QRCode: qr})
}

// handleList returns every session with its live status.
func (h *WhatsAppHandler) handleList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	sessions, err := h.manager.List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "falha ao listar as sessões")
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

// handleDelete disconnects and removes a number.
func (h *WhatsAppHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := h.manager.Disconnect(ctx, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sessão não encontrada")
			return
		}
		writeError(w, http.StatusInternalServerError, "falha ao desconectar a sessão")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
