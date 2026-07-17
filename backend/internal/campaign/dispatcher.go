// Package campaign implements the intelligent WhatsApp dispatch engine. A
// campaign enqueues one durable message row per lead, then a background worker
// drains the queue through a connected WhatsApp number with three anti-ban
// layers:
//
//   - Intelligent delay: a random pause between messages (default 30–90 s).
//   - Per-number rate limiting: a hard hourly ceiling per WhatsApp number.
//   - Number validation: each destination is checked with IsOnWhatsApp before
//     a message is attempted.
//
// The queue lives in Postgres (campaign_messages), so multi-day / multi-week
// blasts survive API restarts. On boot, Start() reclaims stuck SENDING rows and
// wakes a worker per session that still has work.
package campaign

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
)

// Defaults for the anti-ban engine. Overridable via DispatcherConfig / env.
const (
	defaultMinDelay    = 30 * time.Second
	defaultMaxDelay    = 90 * time.Second
	defaultRatePerHour = 30
	defaultMaxWorkers  = 5

	opTimeout         = 45 * time.Second
	dbTimeout         = 10 * time.Second
	idlePollInterval  = 5 * time.Second
	offlineReschedule = 2 * time.Minute
)

// Sentinel errors translated to HTTP statuses by the delivery layer.
var (
	ErrEmptyMessage    = errors.New("message body is required")
	ErrNoSession       = errors.New("whatsapp_session_id is required")
	ErrNoLeads         = errors.New("no phone numbers found for this search")
	ErrSessionNotReady = errors.New("whatsapp number is not connected")
)

// Store persists campaigns and the durable per-recipient queue.
type Store interface {
	Create(ctx context.Context, c *domain.Campaign) error
	EnqueueMessages(ctx context.Context, campaignID string, phones []string) error
	UpdateStatus(ctx context.Context, id, status string) error
	BumpProgress(ctx context.Context, id string, sentDelta, failedDelta int) error
	GetByID(ctx context.Context, id string) (*domain.Campaign, error)
	ListAll(ctx context.Context, limit int) ([]domain.CampaignSummary, error)
	ClaimNext(ctx context.Context, sessionID string) (*domain.CampaignMessage, error)
	MarkMessage(ctx context.Context, id, status, lastError string) error
	RescheduleMessage(ctx context.Context, id string, at time.Time, reason string) error
	ReclaimAllSending(ctx context.Context) (int64, error)
	HasPending(ctx context.Context, campaignID string) (bool, error)
	ListSessionsWithWork(ctx context.Context) ([]string, error)
	NextScheduledAt(ctx context.Context, sessionID string) (time.Time, error)
}

// LeadSource returns the phone numbers a campaign targets.
type LeadSource interface {
	GetPhonesBySearchID(ctx context.Context, searchID string) ([]string, error)
}

// Sender validates and sends through a connected WhatsApp number.
type Sender interface {
	SessionReady(sessionID string) bool
	IsRegistered(ctx context.Context, sessionID, phone string) (jid string, ok bool, err error)
	SendText(ctx context.Context, sessionID, recipientJID, text string) error
}

// Logger is the minimal logging port; *log.Logger satisfies it.
type Logger interface {
	Printf(format string, v ...any)
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

// Config holds anti-ban pacing knobs (typically loaded from env).
type Config struct {
	MinDelay    time.Duration
	MaxDelay    time.Duration
	RatePerHour int
	MaxWorkers  int
}

// Dispatcher owns background session workers and their pacing state.
type Dispatcher struct {
	store  Store
	leads  LeadSource
	sender Sender
	log    Logger

	minDelay    time.Duration
	maxDelay    time.Duration
	ratePerHour int
	maxWorkers  int

	sem chan struct{} // bounds concurrent session workers

	mu       sync.Mutex
	limiters map[string]*rateLimiter
	workers  map[string]struct{} // sessionIDs with a live worker
	lastSent map[string]time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewDispatcher wires the engine with the anti-ban defaults.
func NewDispatcher(store Store, leads LeadSource, sender Sender, logger Logger) *Dispatcher {
	return NewDispatcherWithConfig(store, leads, sender, logger, Config{})
}

// NewDispatcherWithConfig wires the engine with explicit pacing overrides.
// Zero-valued fields fall back to the safe anti-ban defaults.
func NewDispatcherWithConfig(store Store, leads LeadSource, sender Sender, logger Logger, cfg Config) *Dispatcher {
	if logger == nil {
		logger = nopLogger{}
	}
	if cfg.MinDelay <= 0 {
		cfg.MinDelay = defaultMinDelay
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = defaultMaxDelay
	}
	if cfg.MaxDelay < cfg.MinDelay {
		cfg.MaxDelay = cfg.MinDelay
	}
	if cfg.RatePerHour <= 0 {
		cfg.RatePerHour = defaultRatePerHour
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = defaultMaxWorkers
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		store:       store,
		leads:       leads,
		sender:      sender,
		log:         logger,
		minDelay:    cfg.MinDelay,
		maxDelay:    cfg.MaxDelay,
		ratePerHour: cfg.RatePerHour,
		maxWorkers:  cfg.MaxWorkers,
		sem:         make(chan struct{}, cfg.MaxWorkers),
		limiters:    make(map[string]*rateLimiter),
		workers:     make(map[string]struct{}),
		lastSent:    make(map[string]time.Time),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start reclaims stuck SENDING rows from a previous process and wakes a worker
// for every WhatsApp session that still has queued work. Safe to call once
// after WhatsApp sessions have been restored.
func (d *Dispatcher) Start() {
	ctx, cancel := context.WithTimeout(d.ctx, 30*time.Second)
	defer cancel()

	n, err := d.store.ReclaimAllSending(ctx)
	if err != nil {
		d.log.Printf("campaigns: reclaim stuck messages: %v", err)
	} else if n > 0 {
		d.log.Printf("campaigns: reclaimed %d stuck SENDING messages", n)
	}

	sessions, err := d.store.ListSessionsWithWork(ctx)
	if err != nil {
		d.log.Printf("campaigns: list sessions with work: %v", err)
		return
	}
	for _, sid := range sessions {
		d.ensureWorker(sid)
	}
	if len(sessions) > 0 {
		d.log.Printf("campaigns: resumed queue for %d session(s)", len(sessions))
	}
}

// StartCampaign validates the request, persists a PENDING campaign, snapshots
// every normalised phone into campaign_messages, and wakes the session worker.
// It returns as soon as the queue is durable — the HTTP handler never blocks on
// the actual sending.
func (d *Dispatcher) StartCampaign(ctx context.Context, searchID, sessionID, message string) (*domain.Campaign, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, ErrEmptyMessage
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrNoSession
	}
	if !d.sender.SessionReady(sessionID) {
		return nil, ErrSessionNotReady
	}

	rawPhones, err := d.leads.GetPhonesBySearchID(ctx, searchID)
	if err != nil {
		return nil, err
	}
	phones := normalizeAndDedupe(rawPhones)
	if len(phones) == 0 {
		return nil, ErrNoLeads
	}

	c := &domain.Campaign{
		SearchID:          searchID,
		WhatsAppSessionID: sessionID,
		MessageBody:       message,
		Status:            domain.CampaignPending,
		Total:             len(phones),
	}
	if err := d.store.Create(ctx, c); err != nil {
		return nil, err
	}
	if err := d.store.EnqueueMessages(ctx, c.ID, phones); err != nil {
		_ = d.store.UpdateStatus(ctx, c.ID, domain.CampaignCompleted)
		return nil, err
	}

	d.ensureWorker(sessionID)
	d.log.Printf("campaign %s: queued %d messages on session %s", c.ID, len(phones), sessionID)
	return c, nil
}

// GetCampaign returns the current state (including live progress) of a campaign.
func (d *Dispatcher) GetCampaign(ctx context.Context, id string) (*domain.Campaign, error) {
	return d.store.GetByID(ctx, id)
}

// ListCampaigns returns the most recent campaigns enriched with search and session metadata.
func (d *Dispatcher) ListCampaigns(ctx context.Context, limit int) ([]domain.CampaignSummary, error) {
	return d.store.ListAll(ctx, limit)
}

// ensureWorker starts a background drain loop for sessionID if one is not
// already running (bounded by the worker-pool semaphore).
func (d *Dispatcher) ensureWorker(sessionID string) {
	d.mu.Lock()
	if _, ok := d.workers[sessionID]; ok {
		d.mu.Unlock()
		return
	}
	d.workers[sessionID] = struct{}{}
	d.mu.Unlock()

	d.wg.Add(1)
	go d.sessionWorker(sessionID)
}

func (d *Dispatcher) sessionWorker(sessionID string) {
	defer d.wg.Done()

	select {
	case d.sem <- struct{}{}:
	case <-d.ctx.Done():
		d.mu.Lock()
		delete(d.workers, sessionID)
		d.mu.Unlock()
		return
	}
	defer func() { <-d.sem }()

	// clearedSlot is set when we deliberately drop workers[sessionID] before
	// returning so a concurrent StartCampaign can replace us; the deferred
	// cleanup must not wipe the replacement's registration.
	clearedSlot := false
	defer func() {
		if clearedSlot {
			return
		}
		d.mu.Lock()
		delete(d.workers, sessionID)
		d.mu.Unlock()
	}()

	d.log.Printf("campaigns: worker started for session %s", sessionID)
	limiter := d.limiterFor(sessionID)

	for {
		if d.ctx.Err() != nil {
			return
		}

		msg, err := d.claim(sessionID)
		if err != nil {
			d.log.Printf("campaigns: claim on %s failed: %v", sessionID, err)
			if !d.sleep(idlePollInterval) {
				return
			}
			continue
		}
		if msg == nil {
			next, nerr := d.nextDue(sessionID)
			if nerr != nil {
				d.log.Printf("campaigns: next due on %s: %v", sessionID, nerr)
			}
			if next.IsZero() {
				d.mu.Lock()
				delete(d.workers, sessionID)
				clearedSlot = true
				d.mu.Unlock()
				if sessions, err := d.sessionsWithWork(); err == nil {
					for _, sid := range sessions {
						if sid == sessionID {
							d.ensureWorker(sessionID)
							break
						}
					}
				}
				return
			}
			wait := time.Until(next)
			if wait < idlePollInterval {
				wait = idlePollInterval
			}
			if wait > time.Minute {
				wait = time.Minute
			}
			if !d.sleep(wait) {
				return
			}
			continue
		}

		if !d.sender.SessionReady(sessionID) {
			_ = d.reschedule(msg.ID, time.Now().Add(offlineReschedule), "whatsapp session not ready")
			if !d.sleep(idlePollInterval) {
				return
			}
			continue
		}

		if delay := d.paceDelay(sessionID); delay > 0 {
			if !d.sleep(delay) {
				_ = d.reschedule(msg.ID, time.Now(), "shutdown during delay")
				return
			}
		}

		if err := limiter.wait(d.ctx); err != nil {
			_ = d.reschedule(msg.ID, time.Now(), "shutdown during rate limit")
			return
		}

		d.process(msg)
		d.mu.Lock()
		d.lastSent[sessionID] = time.Now()
		d.mu.Unlock()
	}
}

func (d *Dispatcher) sessionsWithWork() ([]string, error) {
	ctx, cancel := context.WithTimeout(d.ctx, dbTimeout)
	defer cancel()
	return d.store.ListSessionsWithWork(ctx)
}

func (d *Dispatcher) process(msg *domain.CampaignMessage) {
	ok, skip, errMsg := d.attempt(msg.WhatsAppSessionID, msg.Phone, msg.MessageBody, msg.CampaignID)

	dbCtx, cancel := context.WithTimeout(d.ctx, dbTimeout)
	defer cancel()

	switch {
	case ok:
		if err := d.store.MarkMessage(dbCtx, msg.ID, domain.MessageSent, ""); err != nil {
			d.log.Printf("campaign %s: mark sent %s: %v", msg.CampaignID, msg.Phone, err)
		}
		if err := d.store.BumpProgress(dbCtx, msg.CampaignID, 1, 0); err != nil {
			d.log.Printf("campaign %s: bump sent: %v", msg.CampaignID, err)
		}
	case skip:
		if err := d.store.MarkMessage(dbCtx, msg.ID, domain.MessageSkipped, errMsg); err != nil {
			d.log.Printf("campaign %s: mark skipped %s: %v", msg.CampaignID, msg.Phone, err)
		}
		if err := d.store.BumpProgress(dbCtx, msg.CampaignID, 0, 1); err != nil {
			d.log.Printf("campaign %s: bump failed(skip): %v", msg.CampaignID, err)
		}
	default:
		if err := d.store.MarkMessage(dbCtx, msg.ID, domain.MessageFailed, errMsg); err != nil {
			d.log.Printf("campaign %s: mark failed %s: %v", msg.CampaignID, msg.Phone, err)
		}
		if err := d.store.BumpProgress(dbCtx, msg.CampaignID, 0, 1); err != nil {
			d.log.Printf("campaign %s: bump failed: %v", msg.CampaignID, err)
		}
	}

	d.maybeComplete(msg.CampaignID)
}

// attempt validates then sends. Returns (sent, skipped, errMsg).
func (d *Dispatcher) attempt(sessionID, phone, message, campaignID string) (sent, skipped bool, errMsg string) {
	ctx, cancel := context.WithTimeout(d.ctx, opTimeout)
	defer cancel()

	jid, ok, err := d.sender.IsRegistered(ctx, sessionID, phone)
	if err != nil {
		d.log.Printf("campaign %s: validate %s failed: %v", campaignID, phone, err)
		return false, false, err.Error()
	}
	if !ok {
		d.log.Printf("campaign %s: %s is not on whatsapp, skipping", campaignID, phone)
		return false, true, "not on whatsapp"
	}

	if err := d.sender.SendText(ctx, sessionID, jid, message); err != nil {
		d.log.Printf("campaign %s: send to %s failed: %v", campaignID, phone, err)
		return false, false, err.Error()
	}
	return true, false, ""
}

func (d *Dispatcher) maybeComplete(campaignID string) {
	ctx, cancel := context.WithTimeout(d.ctx, dbTimeout)
	defer cancel()

	pending, err := d.store.HasPending(ctx, campaignID)
	if err != nil {
		d.log.Printf("campaign %s: has pending: %v", campaignID, err)
		return
	}
	if pending {
		return
	}
	if err := d.store.UpdateStatus(ctx, campaignID, domain.CampaignCompleted); err != nil {
		d.log.Printf("campaign %s: set COMPLETED: %v", campaignID, err)
		return
	}
	if c, err := d.store.GetByID(ctx, campaignID); err == nil {
		d.log.Printf("campaign %s: completed — %d sent, %d failed of %d",
			campaignID, c.Sent, c.Failed, c.Total)
	}
}

func (d *Dispatcher) claim(sessionID string) (*domain.CampaignMessage, error) {
	ctx, cancel := context.WithTimeout(d.ctx, dbTimeout)
	defer cancel()
	return d.store.ClaimNext(ctx, sessionID)
}

func (d *Dispatcher) nextDue(sessionID string) (time.Time, error) {
	ctx, cancel := context.WithTimeout(d.ctx, dbTimeout)
	defer cancel()
	return d.store.NextScheduledAt(ctx, sessionID)
}

func (d *Dispatcher) reschedule(id string, at time.Time, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()
	return d.store.RescheduleMessage(ctx, id, at, reason)
}

// paceDelay returns the remaining jitter since the last successful send on this
// session in this process, or 0 for the first send.
func (d *Dispatcher) paceDelay(sessionID string) time.Duration {
	d.mu.Lock()
	last, ok := d.lastSent[sessionID]
	d.mu.Unlock()
	if !ok {
		return 0
	}
	want := d.randomDelay()
	elapsed := time.Since(last)
	if elapsed >= want {
		return 0
	}
	return want - elapsed
}

func (d *Dispatcher) limiterFor(sessionID string) *rateLimiter {
	d.mu.Lock()
	defer d.mu.Unlock()
	rl, ok := d.limiters[sessionID]
	if !ok {
		rl = newRateLimiter(d.ratePerHour, time.Hour)
		d.limiters[sessionID] = rl
	}
	return rl
}

func (d *Dispatcher) randomDelay() time.Duration {
	if d.maxDelay <= d.minDelay {
		return d.minDelay
	}
	span := int64(d.maxDelay - d.minDelay)
	return d.minDelay + time.Duration(rand.Int63n(span+1))
}

func (d *Dispatcher) sleep(dur time.Duration) bool {
	if dur <= 0 {
		return d.ctx.Err() == nil
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-d.ctx.Done():
		return false
	}
}

// Shutdown cancels in-flight workers and waits up to timeout for them to stop.
// Messages left in SENDING are reclaimed on the next Start().
func (d *Dispatcher) Shutdown(timeout time.Duration) {
	d.cancel()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
