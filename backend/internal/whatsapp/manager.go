// Package whatsapp implements the multi-device WhatsApp session manager built on
// top of whatsmeow (go.mau.fi/whatsmeow). It keeps an in-memory registry of live
// clients (capped at MaxSessions) and mirrors their app-level state — which
// number, current status — into the whatsapp_sessions table via a Store.
//
// whatsmeow owns the cryptographic device/session material in its own sqlstore
// tables; this manager only orchestrates pairing (QR), reconnection on startup,
// and teardown. The two views are linked by the device JID, persisted in the
// session row's session_data.
package whatsapp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/zennitex/clicars-search/internal/domain"
)

const (
	// MaxSessions caps how many WhatsApp numbers can be paired simultaneously.
	MaxSessions = 15

	// qrWaitTimeout bounds how long Connect blocks waiting for whatsmeow to emit
	// the first QR code before giving up.
	qrWaitTimeout = 15 * time.Second

	// qrEventSuccess is the QRChannelItem.Event value emitted once pairing
	// completes (whatsmeow.QRChannelSuccess).
	qrEventSuccess = "success"
)

var (
	// ErrLimitReached is returned by Connect when MaxSessions are already in use.
	ErrLimitReached = errors.New("whatsapp session limit reached")
	// ErrQRUnavailable is returned when whatsmeow fails to produce a QR code.
	ErrQRUnavailable = errors.New("could not generate whatsapp qr code")
	// ErrSessionNotConnected is returned by the messaging methods when the target
	// session has no live, logged-in client (so it can't validate or send).
	ErrSessionNotConnected = errors.New("whatsapp session not connected")
)

// Store is the persistence port the manager depends on, implemented by
// repository.WhatsAppRepository.
type Store interface {
	Insert(ctx context.Context, id, jid, phone, status string) (*domain.WhatsAppSession, error)
	Update(ctx context.Context, id, jid, phone, status string) error
	UpdateStatus(ctx context.Context, id, status string) error
	List(ctx context.Context) ([]domain.WhatsAppSession, error)
	GetByID(ctx context.Context, id string) (*domain.WhatsAppSession, error)
	Delete(ctx context.Context, id string) error
	DeleteOldDisconnected(ctx context.Context, days int) (int64, error)
}

// managedSession pairs a session id with its live whatsmeow client. client is
// nil only for the brief window between reserving a slot and finishing Connect.
type managedSession struct {
	id     string
	client *whatsmeow.Client
}

// Manager owns the registry of live whatsmeow clients and the persistence Store.
type Manager struct {
	mu        sync.Mutex
	sessions  map[string]*managedSession
	container *sqlstore.Container
	store     Store
	log       waLog.Logger
}

// NewManager wires the manager. container is the whatsmeow sqlstore backing the
// device material; store persists the app-level session rows.
func NewManager(container *sqlstore.Container, store Store, log waLog.Logger) *Manager {
	if log == nil {
		log = waLog.Noop
	}
	return &Manager{
		sessions:  make(map[string]*managedSession),
		container: container,
		store:     store,
		log:       log,
	}
}

// Connect starts a new pairing: it spins up a fresh whatsmeow client, asks for a
// QR code, and returns the first code rendered as a base64 PNG data URL together
// with the new session id. Pairing then continues in the background — the caller
// (frontend) polls List until the number appears as CONNECTED. The slot is held
// for the whole pairing attempt and released automatically if it expires.
func (m *Manager) Connect(ctx context.Context) (string, string, error) {
	sessionID := uuid.NewString()

	// Reserve the slot atomically so concurrent Connect calls cannot oversubscribe.
	m.mu.Lock()
	if len(m.sessions) >= MaxSessions {
		m.mu.Unlock()
		return "", "", ErrLimitReached
	}
	m.sessions[sessionID] = &managedSession{id: sessionID}
	m.mu.Unlock()

	deviceStore := m.container.NewDevice()
	client := whatsmeow.NewClient(deviceStore, m.log.Sub("client-"+shortID(sessionID)))

	// The pairing flow must outlive this HTTP request — the user still has to scan
	// the code after we return — so it runs under its own background context.
	qrCtx, cancelQR := context.WithCancel(context.Background())
	qrChan, err := client.GetQRChannel(qrCtx) // must be called before Connect
	if err != nil {
		cancelQR()
		m.removeSession(sessionID)
		return "", "", fmt.Errorf("%w: %v", ErrQRUnavailable, err)
	}
	if err := client.Connect(); err != nil {
		cancelQR()
		m.removeSession(sessionID)
		return "", "", fmt.Errorf("%w: %v", ErrQRUnavailable, err)
	}

	m.mu.Lock()
	if ms, ok := m.sessions[sessionID]; ok {
		ms.client = client
	}
	m.mu.Unlock()

	firstCode := make(chan string, 1)
	go m.pump(sessionID, client, qrChan, cancelQR, firstCode)

	select {
	case code, ok := <-firstCode:
		if !ok {
			return "", "", ErrQRUnavailable // pairing ended before any code
		}
		png, err := qrcode.Encode(code, qrcode.Medium, 256)
		if err != nil {
			cancelQR()
			m.disconnectAndRemove(sessionID, client)
			return "", "", fmt.Errorf("%w: %v", ErrQRUnavailable, err)
		}
		dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		return sessionID, dataURL, nil
	case <-time.After(qrWaitTimeout):
		cancelQR()
		m.disconnectAndRemove(sessionID, client)
		return "", "", ErrQRUnavailable
	case <-ctx.Done():
		cancelQR()
		m.disconnectAndRemove(sessionID, client)
		return "", "", ctx.Err()
	}
}

// pump drains the QR channel: it forwards the first code to Connect, then waits
// for the terminal event (success / timeout / error) to either persist the new
// session or release the reserved slot.
func (m *Manager) pump(sessionID string, client *whatsmeow.Client, qrChan <-chan whatsmeow.QRChannelItem, cancelQR context.CancelFunc, firstCode chan<- string) {
	defer cancelQR()
	sentFirst := false

	for item := range qrChan {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			if !sentFirst {
				sentFirst = true
				firstCode <- item.Code
			}
		case qrEventSuccess:
			m.onPairSuccess(sessionID, client)
			return
		default:
			// timeout, error, err-client-outdated, ... : pairing failed or expired.
			m.log.Warnf("pairing for session %s ended: %s (%v)", sessionID, item.Event, item.Error)
			if !sentFirst {
				close(firstCode)
			}
			m.disconnectAndRemove(sessionID, client)
			return
		}
	}

	if !sentFirst {
		close(firstCode)
	}
	m.disconnectAndRemove(sessionID, client)
}

// onPairSuccess persists the freshly paired number and attaches the long-lived
// status handler. The session id chosen at Connect time becomes the row id, so
// the in-memory registry and the database share one identifier.
func (m *Manager) onPairSuccess(sessionID string, client *whatsmeow.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jid := client.Store.GetJID()
	if _, err := m.store.Insert(ctx, sessionID, jid.String(), jid.User, domain.WhatsAppConnected); err != nil {
		m.log.Errorf("persist whatsapp session %s failed: %v", sessionID, err)
		// Keep the live client regardless; status reconciles on next restart.
	}
	m.attachStatusHandler(sessionID, client)
	m.log.Infof("whatsapp session %s connected as +%s", sessionID, jid.User)
}

// attachStatusHandler reacts to a remote logout (the number was unlinked from
// the phone) by tearing the session down. Transient connect/disconnect blips are
// intentionally ignored here — List reports live status from the client itself.
func (m *Manager) attachStatusHandler(sessionID string, client *whatsmeow.Client) {
	client.AddEventHandler(func(evt any) {
		if _, ok := evt.(*events.LoggedOut); ok {
			m.log.Warnf("whatsapp session %s logged out remotely", sessionID)
			client.Disconnect()
			m.removeSession(sessionID)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := m.store.UpdateStatus(ctx, sessionID, domain.WhatsAppDisconnected); err != nil && !errors.Is(err, domain.ErrNotFound) {
				m.log.Errorf("mark session %s disconnected failed: %v", sessionID, err)
			}
		}
	})
}

// Restore reconnects sessions that were CONNECTED before the last shutdown. It
// matches each persisted row to its whatsmeow device by JID; rows whose device
// material is gone are demoted to DISCONNECTED.
func (m *Manager) Restore(ctx context.Context) error {
	rows, err := m.store.List(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	devices, err := m.container.GetAllDevices(ctx)
	if err != nil {
		return fmt.Errorf("get devices: %w", err)
	}

	byJID := make(map[string]*store.Device, len(devices))
	for _, d := range devices {
		byJID[d.GetJID().String()] = d
	}

	reconnected := 0
	for _, row := range rows {
		if row.Status != domain.WhatsAppConnected {
			continue
		}
		dev, ok := byJID[row.JID]
		if !ok || dev.ID == nil {
			m.log.Warnf("session %s (%s) has no stored device, marking disconnected", row.ID, row.PhoneNumber)
			_ = m.store.UpdateStatus(ctx, row.ID, domain.WhatsAppDisconnected)
			continue
		}
		if err := m.bringUp(row.ID, dev); err != nil {
			m.log.Errorf("reconnect session %s failed: %v", row.ID, err)
			_ = m.store.UpdateStatus(ctx, row.ID, domain.WhatsAppDisconnected)
			continue
		}
		reconnected++
	}
	m.log.Infof("whatsapp: reconnected %d session(s) on startup", reconnected)
	return nil
}

// bringUp connects an already-paired device and registers it in the live map.
func (m *Manager) bringUp(sessionID string, dev *store.Device) error {
	m.mu.Lock()
	if len(m.sessions) >= MaxSessions {
		m.mu.Unlock()
		return ErrLimitReached
	}
	client := whatsmeow.NewClient(dev, m.log.Sub("client-"+shortID(sessionID)))
	m.sessions[sessionID] = &managedSession{id: sessionID, client: client}
	m.mu.Unlock()

	if err := client.Connect(); err != nil {
		m.removeSession(sessionID)
		return err
	}
	m.attachStatusHandler(sessionID, client)
	return nil
}

// List returns every persisted session, overlaying the live connection state
// from the in-memory registry so the UI reflects reality without DB write storms.
func (m *Manager) List(ctx context.Context) ([]domain.WhatsAppSession, error) {
	rows, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range rows {
		ms, ok := m.sessions[rows[i].ID]
		switch {
		case ok && ms.client != nil && ms.client.IsConnected() && ms.client.IsLoggedIn():
			rows[i].Status = domain.WhatsAppConnected
		case ok && ms.client != nil:
			rows[i].Status = domain.WhatsAppConnecting // live but (re)connecting
		default:
			rows[i].Status = domain.WhatsAppDisconnected
		}
	}
	return rows, nil
}

// Disconnect logs the number out of WhatsApp, removes its device material and
// deletes the session row. Returns domain.ErrNotFound for an unknown id.
func (m *Manager) Disconnect(ctx context.Context, id string) error {
	if _, err := m.store.GetByID(ctx, id); err != nil {
		return err // domain.ErrNotFound propagates to a 404
	}

	m.mu.Lock()
	ms := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()

	if ms != nil && ms.client != nil {
		if ms.client.IsLoggedIn() {
			if err := ms.client.Logout(ctx); err != nil { // Logout also deletes the device
				m.log.Warnf("logout session %s: %v", id, err)
				ms.client.Disconnect()
			}
		} else {
			ms.client.Disconnect()
			if ms.client.Store != nil && ms.client.Store.ID != nil {
				if err := ms.client.Store.Delete(ctx); err != nil {
					m.log.Warnf("delete device store for %s: %v", id, err)
				}
			}
		}
	}

	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	m.log.Infof("whatsapp session %s disconnected and removed", id)
	return nil
}

// PurgeExpired removes DISCONNECTED sessions older than `days` (45-day policy).
func (m *Manager) PurgeExpired(ctx context.Context, days int) (int64, error) {
	return m.store.DeleteOldDisconnected(ctx, days)
}

// Shutdown disconnects every live client without logging out, so the same
// devices can be reconnected on the next start (their rows stay CONNECTED).
func (m *Manager) Shutdown() {
	m.mu.Lock()
	clients := make([]*whatsmeow.Client, 0, len(m.sessions))
	for _, ms := range m.sessions {
		if ms.client != nil {
			clients = append(clients, ms.client)
		}
	}
	m.sessions = make(map[string]*managedSession)
	m.mu.Unlock()

	for _, c := range clients {
		c.Disconnect()
	}
}

// --- Messaging (campaign dispatch) -----------------------------------------
//
// These three methods make the Manager satisfy campaign.Sender. They are the
// only place outside pairing/teardown that touches a live client to talk to
// WhatsApp, so they all funnel through liveClient, which enforces that the
// session exists and is connected + logged in.

// liveClient returns the connected, logged-in client for a session id, or
// ErrSessionNotConnected if the session is unknown or not ready.
func (m *Manager) liveClient(id string) (*whatsmeow.Client, error) {
	m.mu.Lock()
	ms, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok || ms.client == nil || !ms.client.IsConnected() || !ms.client.IsLoggedIn() {
		return nil, ErrSessionNotConnected
	}
	return ms.client, nil
}

// SessionReady reports whether a session currently has a live, logged-in client,
// letting the dispatcher fail a campaign fast (clear error) before queuing work
// to a number that can't send.
func (m *Manager) SessionReady(id string) bool {
	_, err := m.liveClient(id)
	return err == nil
}

// IsRegistered checks whether phone (international format, e.g. "+5511…") is on
// WhatsApp using the given session, returning the canonical recipient JID to
// send to. ok=false (nil error) means the number simply isn't on WhatsApp.
func (m *Manager) IsRegistered(ctx context.Context, sessionID, phone string) (string, bool, error) {
	client, err := m.liveClient(sessionID)
	if err != nil {
		return "", false, err
	}
	resp, err := client.IsOnWhatsApp(ctx, []string{phone})
	if err != nil {
		return "", false, fmt.Errorf("is-on-whatsapp: %w", err)
	}
	if len(resp) == 0 || !resp[0].IsIn {
		return "", false, nil
	}
	return resp[0].JID.String(), true, nil
}

// SendText sends a plain-text message to recipientJID (as returned by
// IsRegistered) through the given session.
func (m *Manager) SendText(ctx context.Context, sessionID, recipientJID, text string) error {
	client, err := m.liveClient(sessionID)
	if err != nil {
		return err
	}
	jid, err := types.ParseJID(recipientJID)
	if err != nil {
		return fmt.Errorf("parse recipient jid %q: %w", recipientJID, err)
	}
	if _, err := client.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)}); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}

// disconnectAndRemove tears down a client and frees its slot. Safe to call more
// than once for the same session.
func (m *Manager) disconnectAndRemove(sessionID string, client *whatsmeow.Client) {
	if client != nil {
		client.Disconnect()
	}
	m.removeSession(sessionID)
}

func (m *Manager) removeSession(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
