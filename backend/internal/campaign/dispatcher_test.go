package campaign

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
)

// --- Test doubles for the dispatcher ports ---------------------------------

// memStore is an in-memory, concurrency-safe Store with a durable-style message
// queue. The dispatch worker and the test read it from different goroutines.
type memStore struct {
	mu        sync.Mutex
	campaigns map[string]*domain.Campaign
	messages  []*domain.CampaignMessage
	createErr error
	seq       int
}

func newMemStore() *memStore {
	return &memStore{campaigns: make(map[string]*domain.Campaign)}
}

func (s *memStore) Create(_ context.Context, c *domain.Campaign) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	s.seq++
	c.ID = "camp-" + string(rune('A'+s.seq-1))
	if s.seq > 26 {
		c.ID = "camp-" + time.Now().Format("150405.000")
	}
	c.CreatedAt = time.Now()
	cp := *c
	s.campaigns[c.ID] = &cp
	return nil
}

func (s *memStore) EnqueueMessages(_ context.Context, campaignID string, phones []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.campaigns[campaignID]
	if !ok {
		return domain.ErrNotFound
	}
	now := time.Now()
	for _, phone := range phones {
		s.seq++
		s.messages = append(s.messages, &domain.CampaignMessage{
			ID:                "msg-" + phone + "-" + c.ID,
			CampaignID:        campaignID,
			Phone:             phone,
			Status:            domain.MessagePending,
			ScheduledAt:       now,
			CreatedAt:         now,
			WhatsAppSessionID: c.WhatsAppSessionID,
			MessageBody:       c.MessageBody,
		})
	}
	return nil
}

func (s *memStore) UpdateStatus(_ context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.campaigns[id]
	if !ok {
		return domain.ErrNotFound
	}
	c.Status = status
	return nil
}

func (s *memStore) BumpProgress(_ context.Context, id string, sentDelta, failedDelta int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.campaigns[id]
	if !ok {
		return domain.ErrNotFound
	}
	c.Sent += sentDelta
	c.Failed += failedDelta
	return nil
}

func (s *memStore) GetByID(_ context.Context, id string) (*domain.Campaign, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.campaigns[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *memStore) ListAll(_ context.Context, _ int) ([]domain.CampaignSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.CampaignSummary, 0, len(s.campaigns))
	for _, c := range s.campaigns {
		out = append(out, domain.CampaignSummary{Campaign: *c})
	}
	return out, nil
}

func (s *memStore) ClaimNext(_ context.Context, sessionID string) (*domain.CampaignMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, m := range s.messages {
		if m.Status != domain.MessagePending || m.ScheduledAt.After(now) {
			continue
		}
		c, ok := s.campaigns[m.CampaignID]
		if !ok || c.WhatsAppSessionID != sessionID {
			continue
		}
		if c.Status != domain.CampaignPending && c.Status != domain.CampaignRunning {
			continue
		}
		m.Status = domain.MessageSending
		m.Attempts++
		m.WhatsAppSessionID = c.WhatsAppSessionID
		m.MessageBody = c.MessageBody
		if c.Status == domain.CampaignPending {
			c.Status = domain.CampaignRunning
		}
		cp := *m
		return &cp, nil
	}
	return nil, nil
}

func (s *memStore) MarkMessage(_ context.Context, id, status, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages {
		if m.ID == id {
			m.Status = status
			m.LastError = lastError
			if status == domain.MessageSent {
				now := time.Now()
				m.SentAt = &now
			}
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *memStore) RescheduleMessage(_ context.Context, id string, at time.Time, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages {
		if m.ID == id {
			m.Status = domain.MessagePending
			m.ScheduledAt = at
			m.LastError = reason
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *memStore) ReclaimAllSending(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, m := range s.messages {
		if m.Status == domain.MessageSending {
			m.Status = domain.MessagePending
			m.LastError = "reclaimed on startup"
			n++
		}
	}
	return n, nil
}

func (s *memStore) HasPending(_ context.Context, campaignID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.messages {
		if m.CampaignID == campaignID &&
			(m.Status == domain.MessagePending || m.Status == domain.MessageSending) {
			return true, nil
		}
	}
	return false, nil
}

func (s *memStore) ListSessionsWithWork(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]struct{}{}
	for _, m := range s.messages {
		if m.Status != domain.MessagePending && m.Status != domain.MessageSending {
			continue
		}
		c, ok := s.campaigns[m.CampaignID]
		if !ok || (c.Status != domain.CampaignPending && c.Status != domain.CampaignRunning) {
			continue
		}
		seen[c.WhatsAppSessionID] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out, nil
}

func (s *memStore) NextScheduledAt(_ context.Context, sessionID string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var earliest time.Time
	for _, m := range s.messages {
		if m.Status != domain.MessagePending {
			continue
		}
		c, ok := s.campaigns[m.CampaignID]
		if !ok || c.WhatsAppSessionID != sessionID {
			continue
		}
		if c.Status != domain.CampaignPending && c.Status != domain.CampaignRunning {
			continue
		}
		if earliest.IsZero() || m.ScheduledAt.Before(earliest) {
			earliest = m.ScheduledAt
		}
	}
	return earliest, nil
}

type fakeLeads struct {
	phones []string
	err    error
}

func (l *fakeLeads) GetPhonesBySearchID(context.Context, string) ([]string, error) {
	return l.phones, l.err
}

type fakeSender struct {
	mu      sync.Mutex
	ready   bool
	notOn   map[string]bool  // normalized phones NOT on whatsapp
	sendErr map[string]error // recipient JID -> error to return
	sent    []string         // recipient JIDs successfully sent to
}

func (s *fakeSender) SessionReady(string) bool { return s.ready }

func (s *fakeSender) IsRegistered(_ context.Context, _, phone string) (string, bool, error) {
	if s.notOn[phone] {
		return "", false, nil
	}
	return phone + "@s.whatsapp.net", true, nil
}

func (s *fakeSender) SendText(_ context.Context, _, jid, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sendErr[jid]; err != nil {
		return err
	}
	s.sent = append(s.sent, jid)
	return nil
}

func (s *fakeSender) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

// newTestDispatcher collapses operational delays so tests run instantly while
// still exercising the full delay/rate-limit/validate/send path.
func newTestDispatcher(store Store, leads LeadSource, sender Sender) *Dispatcher {
	d := NewDispatcher(store, leads, sender, nil)
	d.minDelay = 0
	d.maxDelay = 0
	d.ratePerHour = 1000
	return d
}

func waitCompleted(t *testing.T, store *memStore, id string) domain.Campaign {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := store.GetByID(context.Background(), id); err == nil && c.Status == domain.CampaignCompleted {
			return *c
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("campaign did not reach COMPLETED in time")
	return domain.Campaign{}
}

func TestStartCampaign_HappyPath(t *testing.T) {
	store := newMemStore()
	leads := &fakeLeads{phones: []string{"11999990001", "11999990002", "11999990003"}}
	sender := &fakeSender{ready: true}
	d := newTestDispatcher(store, leads, sender)
	defer d.Shutdown(time.Second)

	c, err := d.StartCampaign(context.Background(), "search-1", "session-1", "Olá!", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Status != domain.CampaignPending {
		t.Errorf("expected PENDING on return, got %q", c.Status)
	}
	if c.Total != 3 {
		t.Errorf("expected total 3, got %d", c.Total)
	}

	done := waitCompleted(t, store, c.ID)
	if done.Sent != 3 || done.Failed != 0 {
		t.Errorf("expected 3 sent / 0 failed, got %d/%d", done.Sent, done.Failed)
	}
	if sender.sentCount() != 3 {
		t.Errorf("expected sender to deliver 3 messages, got %d", sender.sentCount())
	}
}

func TestStartCampaign_TwoHundredAuthorizedRecipients(t *testing.T) {
	phones := make([]string, 200)
	for i := range phones {
		phones[i] = fmt.Sprintf("119%08d", i)
	}

	store := newMemStore()
	sender := &fakeSender{ready: true}
	d := newTestDispatcher(store, &fakeLeads{phones: phones}, sender)
	defer d.Shutdown(time.Second)

	c, err := d.StartCampaign(context.Background(), "search-1", "session-1", "Mensagem autorizada", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Total != 200 {
		t.Fatalf("expected 200 queued recipients, got %d", c.Total)
	}

	done := waitCompleted(t, store, c.ID)
	if done.Sent != 200 || done.Failed != 0 {
		t.Fatalf("expected 200 sent / 0 failed, got %d/%d", done.Sent, done.Failed)
	}
}

func TestStartCampaign_SkipsInvalidNumbers(t *testing.T) {
	store := newMemStore()
	leads := &fakeLeads{phones: []string{"11999990001", "11999990002"}}
	sender := &fakeSender{ready: true, notOn: map[string]bool{"+5511999990002": true}}
	d := newTestDispatcher(store, leads, sender)
	defer d.Shutdown(time.Second)

	c, err := d.StartCampaign(context.Background(), "search-1", "session-1", "Oi", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	done := waitCompleted(t, store, c.ID)
	if done.Sent != 1 || done.Failed != 1 {
		t.Errorf("expected 1 sent / 1 failed, got %d/%d", done.Sent, done.Failed)
	}
}

func TestStartCampaign_SendErrorCountsAsFailed(t *testing.T) {
	store := newMemStore()
	leads := &fakeLeads{phones: []string{"11999990001"}}
	sender := &fakeSender{
		ready:   true,
		sendErr: map[string]error{"+5511999990001@s.whatsapp.net": errors.New("network down")},
	}
	d := newTestDispatcher(store, leads, sender)
	defer d.Shutdown(time.Second)

	c, err := d.StartCampaign(context.Background(), "search-1", "session-1", "Oi", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	done := waitCompleted(t, store, c.ID)
	if done.Sent != 0 || done.Failed != 1 {
		t.Errorf("expected 0 sent / 1 failed, got %d/%d", done.Sent, done.Failed)
	}
}

func TestStartCampaign_NormalizesAndDedupes(t *testing.T) {
	store := newMemStore()
	leads := &fakeLeads{phones: []string{"11999990001", "(11) 99999-0001", "5511999990001", ""}}
	sender := &fakeSender{ready: true}
	d := newTestDispatcher(store, leads, sender)
	defer d.Shutdown(time.Second)

	c, err := d.StartCampaign(context.Background(), "search-1", "session-1", "Oi", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Total != 1 {
		t.Fatalf("expected total 1 after dedupe, got %d", c.Total)
	}

	done := waitCompleted(t, store, c.ID)
	if done.Sent != 1 {
		t.Errorf("expected 1 sent, got %d", done.Sent)
	}
}

func TestStartCampaign_RequiresConsentConfirmation(t *testing.T) {
	store := newMemStore()
	d := newTestDispatcher(
		store,
		&fakeLeads{phones: []string{"11999990001"}},
		&fakeSender{ready: true},
	)
	defer d.Shutdown(time.Second)

	_, err := d.StartCampaign(context.Background(), "search-1", "session-1", "Oi", false)
	if !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("expected %v, got %v", ErrConsentRequired, err)
	}
	if len(store.campaigns) != 0 {
		t.Fatal("campaign must not be persisted without consent confirmation")
	}
}

func TestStartCampaign_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		sessionID string
		ready     bool
		phones    []string
		wantErr   error
	}{
		{"empty message", "   ", "session-1", true, []string{"11999990001"}, ErrEmptyMessage},
		{"no session", "Oi", "", true, []string{"11999990001"}, ErrNoSession},
		{"session not ready", "Oi", "session-1", false, []string{"11999990001"}, ErrSessionNotReady},
		{"no leads", "Oi", "session-1", true, []string{}, ErrNoLeads},
		{"only junk leads", "Oi", "session-1", true, []string{"", "abc"}, ErrNoLeads},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			d := newTestDispatcher(store, &fakeLeads{phones: tc.phones}, &fakeSender{ready: tc.ready})
			defer d.Shutdown(time.Second)

			_, err := d.StartCampaign(context.Background(), "search-1", tc.sessionID, tc.message, true)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestStart_ResumesAfterRestart seeds a durable queue mid-flight (1 SENT, 1
// stuck SENDING, 1 PENDING) and asserts Start() reclaims + drains the rest.
func TestStart_ResumesAfterRestart(t *testing.T) {
	store := newMemStore()
	sender := &fakeSender{ready: true}

	c := &domain.Campaign{
		ID:                "camp-resume",
		SearchID:          "search-1",
		WhatsAppSessionID: "session-1",
		MessageBody:       "Oi",
		Status:            domain.CampaignRunning,
		Total:             3,
		Sent:              1,
		Failed:            0,
		CreatedAt:         time.Now(),
	}
	store.mu.Lock()
	store.campaigns[c.ID] = c
	now := time.Now()
	store.messages = []*domain.CampaignMessage{
		{
			ID: "m1", CampaignID: c.ID, Phone: "+5511999990001",
			Status: domain.MessageSent, ScheduledAt: now, CreatedAt: now,
		},
		{
			ID: "m2", CampaignID: c.ID, Phone: "+5511999990002",
			Status: domain.MessageSending, ScheduledAt: now, CreatedAt: now,
			Attempts: 1,
		},
		{
			ID: "m3", CampaignID: c.ID, Phone: "+5511999990003",
			Status: domain.MessagePending, ScheduledAt: now, CreatedAt: now,
		},
	}
	store.mu.Unlock()

	d := newTestDispatcher(store, &fakeLeads{}, sender)
	defer d.Shutdown(time.Second)
	d.Start()

	done := waitCompleted(t, store, c.ID)
	if done.Sent != 3 || done.Failed != 0 {
		t.Errorf("expected 3 sent / 0 failed after resume, got %d/%d", done.Sent, done.Failed)
	}
	if sender.sentCount() != 2 {
		t.Errorf("expected 2 new sends (reclaimed + pending), got %d", sender.sentCount())
	}
}
