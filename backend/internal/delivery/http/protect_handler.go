package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zennitex/clicars-search/internal/protect"
)

// ProtectService is implemented by protect.Engine.
type ProtectService interface {
	Snapshots(ctx context.Context) ([]protect.Snapshot, error)
	Pause(ctx context.Context, sessionID string) error
	Resume(ctx context.Context, sessionID string) error
	ListContacts(ctx context.Context) ([]protect.Contact, error)
	AddContact(ctx context.Context, phone, label string) (*protect.Contact, error)
	DeleteContact(ctx context.Context, id string) error
}

type ProtectHandler struct {
	svc ProtectService
}

func NewProtectHandler(svc ProtectService) *ProtectHandler {
	return &ProtectHandler{svc: svc}
}

func (h *ProtectHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/protect/numbers", h.handleList)
	mux.HandleFunc("POST /api/v1/protect/numbers/{id}/pause", h.handlePause)
	mux.HandleFunc("POST /api/v1/protect/numbers/{id}/resume", h.handleResume)
	mux.HandleFunc("GET /api/v1/protect/contacts", h.handleListContacts)
	mux.HandleFunc("POST /api/v1/protect/contacts", h.handleAddContact)
	mux.HandleFunc("DELETE /api/v1/protect/contacts/{id}", h.handleDeleteContact)
}

func (h *ProtectHandler) handleList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	snaps, err := h.svc.Snapshots(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "falha ao carregar a saúde dos números")
		return
	}
	if snaps == nil {
		snaps = []protect.Snapshot{}
	}
	writeJSON(w, http.StatusOK, snaps)
}

func (h *ProtectHandler) handlePause(w http.ResponseWriter, r *http.Request) {
	h.flip(w, r, true)
}

func (h *ProtectHandler) handleResume(w http.ResponseWriter, r *http.Request) {
	h.flip(w, r, false)
}

func (h *ProtectHandler) flip(w http.ResponseWriter, r *http.Request, pause bool) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var err error
	if pause {
		err = h.svc.Pause(ctx, id)
	} else {
		err = h.svc.Resume(ctx, id)
	}
	if err != nil {
		if errors.Is(err, protect.ErrProfileNotFound) {
			writeError(w, http.StatusNotFound, "número não encontrado no motor de proteção")
			return
		}
		writeError(w, http.StatusInternalServerError, "falha ao atualizar o número")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ProtectHandler) handleListContacts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	list, err := h.svc.ListContacts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "falha ao listar contatos de aquecimento")
		return
	}
	if list == nil {
		list = []protect.Contact{}
	}
	writeJSON(w, http.StatusOK, list)
}

type contactRequest struct {
	Phone string `json:"phone"`
	Label string `json:"label"`
}

func (h *ProtectHandler) handleAddContact(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req contactRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	c, err := h.svc.AddContact(ctx, strings.TrimSpace(req.Phone), strings.TrimSpace(req.Label))
	if err != nil {
		if errors.Is(err, protect.ErrInvalidContact) {
			writeError(w, http.StatusBadRequest, "informe um telefone válido com DDD")
			return
		}
		writeError(w, http.StatusInternalServerError, "falha ao salvar o contato de aquecimento")
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *ProtectHandler) handleDeleteContact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := h.svc.DeleteContact(ctx, id); err != nil {
		if errors.Is(err, protect.ErrContactNotFound) {
			writeError(w, http.StatusNotFound, "contato não encontrado")
			return
		}
		writeError(w, http.StatusInternalServerError, "falha ao remover o contato")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
