// Package whatsapp integrates Clicars Search with the hosted Zennitex WhatsApp
// API (https://whatsapp.zennitex.com.br). Pairing, connection state and message
// delivery all go through that service — this package is a thin adapter that
// keeps the Clicars HTTP surface (/api/v1/whatsapp/*, campaign Sender) stable.
package whatsapp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/zennitex/clicars-search/internal/domain"
)

const (
	// MaxSessions caps how many WhatsApp numbers Clicars may pair at once.
	MaxSessions = 15
)

var (
	ErrLimitReached        = errors.New("whatsapp session limit reached")
	ErrQRUnavailable       = errors.New("could not generate whatsapp qr code")
	ErrSessionNotConnected = errors.New("whatsapp session not connected")
	ErrNotFound            = errors.New("whatsapp instance not found")
	ErrAlreadyConnected    = errors.New("whatsapp instance already connected")
	ErrNotConfigured       = errors.New("whatsapp api not configured")
)

// Logger is the minimal logging port; *log.Logger satisfies it.
type Logger interface {
	Printf(format string, v ...any)
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

// Manager proxies session lifecycle and messaging to the Zennitex WhatsApp API.
type Manager struct {
	api    *APIClient
	log    Logger
	prefix string // instance name prefix when creating slots

	mu     sync.RWMutex
	tokens map[string]string // instanceID → bearer token (from create/list)
}

// NewManager builds a remote WhatsApp manager. baseURL must point at the API
// root including /api (e.g. https://whatsapp.zennitex.com.br/api).
func NewManager(baseURL, adminKey string, logger Logger) (*Manager, error) {
	baseURL = strings.TrimSpace(baseURL)
	adminKey = strings.TrimSpace(adminKey)
	if baseURL == "" || adminKey == "" {
		return nil, ErrNotConfigured
	}
	if logger == nil {
		logger = nopLogger{}
	}
	return &Manager{
		api:    NewAPIClient(baseURL, adminKey),
		log:    logger,
		prefix: "Clicars Search",
		tokens: make(map[string]string),
	}, nil
}

// Connect creates a remote instance and returns its id plus a PNG QR data URL
// ready for <img src>. Pairing continues on the remote API; the UI polls List.
func (m *Manager) Connect(ctx context.Context) (string, string, error) {
	instances, err := m.api.ListInstances(ctx)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrQRUnavailable, err)
	}
	if len(instances) >= MaxSessions {
		return "", "", ErrLimitReached
	}

	name := fmt.Sprintf("%s #%d", m.prefix, len(instances)+1)
	inst, err := m.api.CreateInstance(ctx, name)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrQRUnavailable, err)
	}
	m.cacheToken(inst.ID, inst.Token)

	qr, err := m.api.GetQR(ctx, inst.ID)
	if err != nil {
		// Best-effort cleanup so a failed QR does not burn a slot.
		_ = m.api.DeleteInstance(context.Background(), inst.ID)
		m.forgetToken(inst.ID)
		if errors.Is(err, ErrAlreadyConnected) {
			return "", "", err
		}
		return "", "", fmt.Errorf("%w: %v", ErrQRUnavailable, err)
	}
	if strings.TrimSpace(qr.QRCode) == "" {
		_ = m.api.DeleteInstance(context.Background(), inst.ID)
		m.forgetToken(inst.ID)
		return "", "", ErrQRUnavailable
	}

	png, err := qrcode.Encode(qr.QRCode, qrcode.Medium, 256)
	if err != nil {
		_ = m.api.DeleteInstance(context.Background(), inst.ID)
		m.forgetToken(inst.ID)
		return "", "", fmt.Errorf("%w: %v", ErrQRUnavailable, err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	m.log.Printf("whatsapp: created remote instance %s (%s)", inst.ID, name)
	return inst.ID, dataURL, nil
}

// List returns every remote instance mapped to the Clicars session shape.
func (m *Manager) List(ctx context.Context) ([]domain.WhatsAppSession, error) {
	instances, err := m.api.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.WhatsAppSession, 0, len(instances))
	for _, inst := range instances {
		m.cacheToken(inst.ID, inst.Token)
		out = append(out, mapInstance(inst))
	}
	return out, nil
}

// Disconnect deletes the remote instance (logout + remove).
func (m *Manager) Disconnect(ctx context.Context, id string) error {
	if err := m.api.DeleteInstance(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.ErrNotFound
		}
		return err
	}
	m.forgetToken(id)
	m.log.Printf("whatsapp: deleted remote instance %s", id)
	return nil
}

// Restore is a no-op: sessions live on the remote API and reconnect there.
func (m *Manager) Restore(context.Context) error { return nil }

// PurgeExpired is a no-op: retention is owned by the WhatsApp API service.
func (m *Manager) PurgeExpired(context.Context, int) (int64, error) { return 0, nil }

// Shutdown is a no-op for the remote adapter (no local sockets to close).
func (m *Manager) Shutdown() {}

// --- campaign.Sender --------------------------------------------------------

// SessionReady reports whether the remote instance is connected.
func (m *Manager) SessionReady(id string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	inst, err := m.api.GetInstance(ctx, id)
	if err != nil {
		return false
	}
	m.cacheToken(inst.ID, inst.Token)
	return mapStatus(inst.Status) == domain.WhatsAppConnected
}

// IsRegistered normalises the phone for send. The remote API has no
// IsOnWhatsApp endpoint, so validity is confirmed when SendText runs.
func (m *Manager) IsRegistered(_ context.Context, _, phone string) (string, bool, error) {
	d := digitsOnly(phone)
	if d == "" {
		return "", false, nil
	}
	return d, true, nil
}

// SendText sends a plain-text message through the remote instance token.
// recipient may be digits or a JID; both are normalised to digits for the API.
func (m *Manager) SendText(ctx context.Context, sessionID, recipient, text string) error {
	token, err := m.tokenFor(ctx, sessionID)
	if err != nil {
		return err
	}
	to := digitsOnly(recipient)
	if to == "" {
		return fmt.Errorf("invalid recipient %q", recipient)
	}
	if _, err := m.api.SendText(ctx, token, to, text); err != nil {
		return err
	}
	return nil
}

func (m *Manager) tokenFor(ctx context.Context, id string) (string, error) {
	m.mu.RLock()
	tok := m.tokens[id]
	m.mu.RUnlock()
	if tok != "" {
		return tok, nil
	}
	inst, err := m.api.GetInstance(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrSessionNotConnected
		}
		return "", err
	}
	if inst.Token == "" {
		return "", ErrSessionNotConnected
	}
	m.cacheToken(inst.ID, inst.Token)
	return inst.Token, nil
}

func (m *Manager) cacheToken(id, token string) {
	if id == "" || token == "" {
		return
	}
	m.mu.Lock()
	m.tokens[id] = token
	m.mu.Unlock()
}

func (m *Manager) forgetToken(id string) {
	m.mu.Lock()
	delete(m.tokens, id)
	m.mu.Unlock()
}

func mapInstance(inst remoteInstance) domain.WhatsAppSession {
	return domain.WhatsAppSession{
		ID:          inst.ID,
		JID:         inst.DeviceJID,
		PhoneNumber: digitsOnly(inst.PhoneNumber),
		Status:      mapStatus(inst.Status),
		CreatedAt:   inst.CreatedAt,
	}
}

func mapStatus(remote string) string {
	switch strings.ToLower(strings.TrimSpace(remote)) {
	case "connected":
		return domain.WhatsAppConnected
	case "connecting", "qr_pending":
		return domain.WhatsAppConnecting
	default:
		return domain.WhatsAppDisconnected
	}
}

func digitsOnly(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Ensure *log.Logger satisfies Logger without importing in tests unnecessarily.
var _ Logger = (*log.Logger)(nil)
