package protect

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
)

const (
	defaultTimezone  = "America/Sao_Paulo"
	defaultRestEvery = 10
	matureWarmupMax  = 2
	seedPrefix       = "seed:"
)

// ErrProfileNotFound is returned by Pause/Resume when the session has no health row.
var ErrProfileNotFound = errors.New("whatsapp number profile not found")

// Config holds operational pacing for a single WhatsApp number.
type Config struct {
	Timezone          string
	BusinessStartHour int
	BusinessEndHour   int
	LunchFrom         int
	LunchTo           int
	DailyTarget       int
	MinDelay          time.Duration
	MaxDelay          time.Duration
	RestEvery         int
	RestMin           time.Duration
	RestMax           time.Duration
	HumanPauseChance  int // 0-100, extra 2–7 min pause
	WeekendFactor     float64
	MaturePhones      []string
	PrimaryPhones     []string
	WarmupInterval    time.Duration
	WarmupTick        time.Duration
}

func (c Config) withDefaults() Config {
	if c.Timezone == "" {
		c.Timezone = defaultTimezone
	}
	if c.BusinessStartHour == 0 && c.BusinessEndHour == 0 {
		c.BusinessStartHour, c.BusinessEndHour = 8, 20
	}
	if c.LunchFrom == 0 && c.LunchTo == 0 {
		c.LunchFrom, c.LunchTo = 12, 13
	}
	if c.DailyTarget <= 0 {
		c.DailyTarget = DefaultTargetDaily
	}
	if c.MinDelay <= 0 {
		c.MinDelay = 90 * time.Second
	}
	if c.MaxDelay < c.MinDelay {
		c.MaxDelay = 4 * time.Minute
	}
	if c.RestEvery <= 0 {
		c.RestEvery = defaultRestEvery
	}
	if c.RestMin <= 0 {
		c.RestMin = 4 * time.Minute
	}
	if c.RestMax < c.RestMin {
		c.RestMax = 12 * time.Minute
	}
	if c.HumanPauseChance <= 0 {
		c.HumanPauseChance = 12
	}
	if c.WeekendFactor <= 0 {
		c.WeekendFactor = 0.55
	}
	if c.WarmupInterval <= 0 {
		c.WarmupInterval = 8 * time.Minute
	}
	if c.WarmupTick <= 0 {
		c.WarmupTick = 3 * time.Minute
	}
	return c
}

func (c Config) hours() Hours {
	return Hours{
		StartHour: c.BusinessStartHour,
		EndHour:   c.BusinessEndHour,
		LunchFrom: c.LunchFrom,
		LunchTo:   c.LunchTo,
	}.normalize()
}

// SessionSource lists live WhatsApp sessions so profiles stay in sync.
type SessionSource interface {
	List(ctx context.Context) ([]domain.WhatsAppSession, error)
}

// Sender is the warmup worker's send port (subset of campaign.Sender).
type Sender interface {
	SessionReady(sessionID string) bool
	SendText(ctx context.Context, sessionID, recipient, text string) error
}

// Logger is the minimal logging port; *log.Logger satisfies it.
type Logger interface {
	Printf(format string, v ...any)
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

// Engine is the anti-ban gate + automatic warmup controller.
type Engine struct {
	store  Store
	clock  Clock
	cfg    Config
	log    Logger
	source SessionSource
	sender Sender

	mu       sync.Mutex
	lastWarm map[string]string // sessionID → last warmup phrase

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine wires the protection engine. clock may be nil (real local clock).
func NewEngine(store Store, cfg Config, logger Logger, clock Clock) *Engine {
	cfg = cfg.withDefaults()
	if logger == nil {
		logger = nopLogger{}
	}
	if clock == nil {
		clock = newRealClock(cfg.Timezone)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		store:    store,
		clock:    clock,
		cfg:      cfg,
		log:      logger,
		lastWarm: make(map[string]string),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// AttachLiveWires the WhatsApp manager used for sync + warmup sends.
func (e *Engine) Attach(source SessionSource, sender Sender) {
	e.source = source
	e.sender = sender
}

// Start seeds mature numbers, then runs the warmup worker until Shutdown.
func (e *Engine) Start() {
	ctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	if err := e.SeedPrimary(ctx); err != nil {
		e.log.Printf("protect: seed primary numbers: %v", err)
	}
	if err := e.SyncSessions(ctx); err != nil {
		e.log.Printf("protect: initial session sync: %v", err)
	}

	e.wg.Add(1)
	go e.loop()
	e.log.Printf("protect: engine started (target %d/day, window %02d–%02d %s)",
		e.cfg.DailyTarget, e.cfg.BusinessStartHour, e.cfg.BusinessEndHour, e.cfg.Timezone)
}

func (e *Engine) loop() {
	defer e.wg.Done()
	tick := time.NewTicker(e.cfg.WarmupTick)
	defer tick.Stop()
	syncTick := time.NewTicker(45 * time.Second)
	defer syncTick.Stop()

	e.runWarmupPass()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-tick.C:
			e.runWarmupPass()
		case <-syncTick.C:
			ctx, cancel := context.WithTimeout(e.ctx, 15*time.Second)
			if err := e.SyncSessions(ctx); err != nil {
				e.log.Printf("protect: session sync: %v", err)
			}
			cancel()
		}
	}
}

// Shutdown stops the warmup worker.
func (e *Engine) Shutdown(timeout time.Duration) {
	e.cancel()
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// SeedPrimary creates (or resets) profiles for the numbers that must walk the
// full 21-day ramp. A previous "mature skip" is undone so the chip starts at day 1.
func (e *Engine) SeedPrimary(ctx context.Context) error {
	now := e.clock.Now()
	phones := e.cfg.PrimaryPhones
	if len(phones) == 0 {
		phones = []string{PrimaryPhone}
	}
	for _, raw := range phones {
		phone := digitsOnly(raw)
		if phone == "" {
			continue
		}
		existing, err := e.store.GetProfileByPhone(ctx, phone)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.MatureSeed || existing.Stage == StageMature && daysSince(existing.WarmupStartedAt, now)+1 < WarmupDays {
				existing.Stage = StageWarming
				existing.WarmupDay = 1
				existing.WarmupStartedAt = now
				existing.MatureSeed = false
				existing.HealthScore = 72
				existing.CircuitOpenUntil = nil
				existing.ConsecutiveErrors = 0
			}
			e.refreshDay(existing, now)
			if err := e.store.UpsertProfile(ctx, existing); err != nil {
				return err
			}
			e.log.Printf("protect: primary %s on warmup day %d/%d (%s)", phone, existing.WarmupDay, WarmupDays, PhaseLabel(PhaseForDay(existing.WarmupDay)))
			continue
		}
		p := e.newProfile(seedPrefix+phone, phone, now)
		if err := e.store.UpsertProfile(ctx, p); err != nil {
			return err
		}
		e.log.Printf("protect: seeded primary %s at warmup day 1 (%s, cap %d)", phone, PhaseLabel(PhaseFoundation), p.DailyCap)
	}
	return nil
}

// SyncSessions upserts a profile for every live WhatsApp session and rebinds
// seed:* rows when the real number comes online.
func (e *Engine) SyncSessions(ctx context.Context) error {
	if e.source == nil {
		return nil
	}
	sessions, err := e.source.List(ctx)
	if err != nil {
		return err
	}
	now := e.clock.Now()
	for _, s := range sessions {
		phone := digitsOnly(s.PhoneNumber)
		if err := e.ensureSession(ctx, s.ID, phone, now); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) ensureSession(ctx context.Context, sessionID, phone string, now time.Time) error {
	if sessionID == "" {
		return nil
	}
	p, err := e.store.GetProfile(ctx, sessionID)
	if err != nil {
		return err
	}
	if p == nil && phone != "" {
		byPhone, err := e.store.GetProfileByPhone(ctx, phone)
		if err != nil {
			return err
		}
		if byPhone != nil && byPhone.SessionID != sessionID {
			if err := e.store.RebindSession(ctx, byPhone.SessionID, sessionID); err != nil {
				return err
			}
			byPhone.SessionID = sessionID
			p = byPhone
		}
	}
	if p == nil {
		p = e.newProfile(sessionID, phone, now)
	}
	if phone != "" {
		p.PhoneNumber = phone
	}
	if e.isMaturePhone(p.PhoneNumber) {
		e.promoteMature(p, now)
	}
	e.refreshDay(p, now)
	return e.store.UpsertProfile(ctx, p)
}

func (e *Engine) newProfile(sessionID, phone string, now time.Time) *Profile {
	p := &Profile{
		SessionID:       sessionID,
		PhoneNumber:     digitsOnly(phone),
		Stage:           StageWarming,
		WarmupStartedAt: now,
		WarmupDay:       1,
		TargetDaily:     e.cfg.DailyTarget,
		HealthScore:     72,
		SentTodayDate:   startOfDay(now),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if e.isMaturePhone(p.PhoneNumber) {
		e.promoteMature(p, now)
	} else {
		p.DailyCap = e.calendarCap(p, now)
	}
	return p
}

func (e *Engine) promoteMature(p *Profile, now time.Time) {
	p.Stage = StageMature
	p.WarmupDay = WarmupDays
	p.TargetDaily = e.cfg.DailyTarget
	p.DailyCap = e.calendarCap(p, now)
	if p.HealthScore < 90 {
		p.HealthScore = 96
	}
	p.MatureSeed = true
}

func (e *Engine) isMaturePhone(phone string) bool {
	d := digitsOnly(phone)
	if d == "" {
		return false
	}
	for _, raw := range e.cfg.MaturePhones {
		if digitsOnly(raw) == d {
			return true
		}
	}
	return false
}

func (e *Engine) refreshDay(p *Profile, now time.Time) {
	todayKey := now.Format("2006-01-02")
	storedKey := p.SentTodayDate.Format("2006-01-02")
	if p.SentTodayDate.IsZero() || storedKey != todayKey {
		p.SentToday = 0
		p.WarmupSentToday = 0
		p.BurstCount = 0
		p.SentTodayDate = startOfDay(now)
	}
	elapsed := daysSince(p.WarmupStartedAt, now) + 1
	if elapsed > p.WarmupDay {
		p.WarmupDay = elapsed
	}
	if p.WarmupDay > WarmupDays && p.Stage == StageWarming {
		p.Stage = StageMature
	}
	if p.Stage == StageCooling && p.CircuitOpenUntil != nil && now.After(*p.CircuitOpenUntil) {
		p.Stage = StageWarming
		if p.WarmupDay < 7 {
			p.WarmupDay = 7
		}
		p.CircuitOpenUntil = nil
	}
	if p.Stage != StagePaused {
		p.DailyCap = e.calendarCap(p, now)
		if p.HealthScore < 40 {
			p.DailyCap = p.DailyCap / 2
			if p.DailyCap < 4 {
				p.DailyCap = 4
			}
		}
	}
}

func (e *Engine) calendarCap(p *Profile, now time.Time) int {
	day := p.WarmupDay
	if p.Stage == StageMature || e.isMaturePhone(p.PhoneNumber) {
		day = WarmupDays
	}
	base := DailyCapForDay(day, p.TargetDaily)
	if p.TargetDaily <= 0 {
		base = DailyCapForDay(day, e.cfg.DailyTarget)
	}
	return ApplyCalendar(base, int(now.Weekday()), e.cfg.WeekendFactor)
}

// Evaluate is the campaign.Guard hook: it never sends, it only says wait/allow.
func (e *Engine) Evaluate(ctx context.Context, sessionID string) (Decision, error) {
	return e.evaluate(ctx, sessionID, KindCampaign)
}

func (e *Engine) evaluate(ctx context.Context, sessionID, kind string) (Decision, error) {
	now := e.clock.Now()
	p, err := e.store.GetProfile(ctx, sessionID)
	if err != nil {
		return Decision{}, err
	}
	if p == nil {
		p = e.newProfile(sessionID, "", now)
		if err := e.store.UpsertProfile(ctx, p); err != nil {
			return Decision{}, err
		}
	}
	e.refreshDay(p, now)
	_ = e.store.UpsertProfile(ctx, p)

	if p.Stage == StagePaused {
		return Decision{Wait: 15 * time.Minute, Reason: "número pausado manualmente"}, nil
	}
	if p.CircuitOpenUntil != nil && now.Before(*p.CircuitOpenUntil) {
		return Decision{Wait: p.CircuitOpenUntil.Sub(now), Reason: "circuito aberto após falhas"}, nil
	}

	ok, next := e.cfg.hours().inWindow(now)
	if !ok {
		wait := next.Sub(now)
		if wait < time.Minute {
			wait = time.Minute
		}
		return Decision{Wait: wait, Reason: "fora da janela de envio"}, nil
	}

	if p.SentToday >= p.DailyCap {
		tomorrow := startOfDay(now).Add(24 * time.Hour)
		open := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(),
			e.cfg.BusinessStartHour, 0, 0, 0, e.clock.Location())
		wait := open.Sub(now)
		if wait < time.Minute {
			wait = time.Minute
		}
		return Decision{Wait: wait, Reason: "teto diário atingido"}, nil
	}

	hourlyCap := HourlyCapFor(p.DailyCap, e.cfg.hours().businessSpan())
	hourly, err := e.store.CountSuccessSince(ctx, sessionID, now.Add(-time.Hour))
	if err != nil {
		return Decision{}, err
	}
	if hourly >= hourlyCap {
		e.log.Printf("protect: hourly gate %s: %d/%d since %v (%s)", sessionID, hourly, hourlyCap, now.Add(-time.Hour), e.clock.Now())
		return Decision{Wait: 4 * time.Minute, Reason: "teto horário atingido"}, nil
	}

	if kind == KindCampaign {
		budget := CampaignBudget(p.WarmupDay, p.DailyCap)
		campaignToday := p.SentToday - p.WarmupSentToday
		if campaignToday < 0 {
			campaignToday = 0
		}
		if campaignToday >= budget {
			tomorrow := startOfDay(now).Add(24 * time.Hour)
			open := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(),
				e.cfg.BusinessStartHour, 0, 0, 0, e.clock.Location())
			wait := open.Sub(now)
			if wait < time.Minute {
				wait = time.Minute
			}
			reason := "fase de aquecimento — campanhas ainda não liberadas"
			if budget > 0 {
				reason = "cota de campanha da fase esgotada"
			}
			return Decision{Wait: wait, Reason: reason}, nil
		}
	}

	if kind == KindWarmup {
		maxWarm := WarmupBudget(p.WarmupDay, p.DailyCap)
		if p.Stage == StageMature {
			maxWarm = matureWarmupMax
		}
		if p.WarmupSentToday >= maxWarm {
			return Decision{Wait: time.Hour, Reason: "cota de aquecimento do dia esgotada"}, nil
		}
	}

	restEvery := e.restEveryFor(p)
	if p.BurstCount >= restEvery {
		rest := e.cfg.RestMin
		span := int64(e.cfg.RestMax - e.cfg.RestMin)
		if span > 0 {
			rest += time.Duration(rand.Int63n(span + 1))
		}
		p.BurstCount = 0
		_ = e.store.UpsertProfile(ctx, p)
		return Decision{Wait: rest, Reason: "pausa de descanso"}, nil
	}

	wait := e.jitterFor(p)
	if rand.Intn(100) < e.cfg.HumanPauseChance {
		wait += 2*time.Minute + time.Duration(rand.Intn(5))*time.Minute
	}
	return Decision{Allow: true, Wait: wait, Reason: "ok"}, nil
}

func (e *Engine) restEveryFor(p *Profile) int {
	if e.cfg.RestEvery >= 100 {
		return e.cfg.RestEvery // tests collapse rests
	}
	switch PhaseForDay(p.WarmupDay) {
	case PhaseFoundation:
		return 4
	case PhasePresence:
		return 6
	default:
		return e.cfg.RestEvery
	}
}

func (e *Engine) jitterFor(p *Profile) time.Duration {
	min, max := e.cfg.MinDelay, e.cfg.MaxDelay
	if min >= time.Second {
		switch PhaseForDay(p.WarmupDay) {
		case PhaseFoundation:
			min, max = 4*time.Minute, 10*time.Minute
		case PhasePresence:
			min, max = 3*time.Minute, 8*time.Minute
		case PhaseMix:
			min, max = 2*time.Minute, 6*time.Minute
		}
	}
	if max <= min {
		return min
	}
	span := int64(max - min)
	return min + time.Duration(rand.Int63n(span+1))
}

// Record updates health after a real send attempt (not a skip).
func (e *Engine) Record(ctx context.Context, sessionID string, success bool, errMsg string) {
	e.record(ctx, sessionID, KindCampaign, "", success, errMsg)
}

func (e *Engine) record(ctx context.Context, sessionID, kind, phone string, success bool, errMsg string) {
	now := e.clock.Now()
	p, err := e.store.GetProfile(ctx, sessionID)
	if err != nil {
		e.log.Printf("protect: load profile %s: %v", sessionID, err)
		return
	}
	if p == nil {
		p = e.newProfile(sessionID, "", now)
	}
	e.refreshDay(p, now)

	_ = e.store.InsertEvent(ctx, Event{
		SessionID: sessionID,
		Kind:      kind,
		Phone:     digitsOnly(phone),
		Success:   success,
		Error:     errMsg,
		CreatedAt: now,
	})

	if success {
		p.SentToday++
		p.BurstCount++
		p.ConsecutiveErrors = 0
		p.LastError = ""
		t := now
		p.LastSentAt = &t
		if p.HealthScore < 100 {
			p.HealthScore++
		}
		if kind == KindWarmup {
			p.WarmupSentToday++
		}
		if p.Stage == StageCooling && p.HealthScore >= 60 {
			p.Stage = StageWarming
		}
	} else {
		p.ConsecutiveErrors++
		p.LastError = errMsg
		sig := classifyError(errMsg)
		switch sig {
		case SignalBan:
			p.HealthScore -= 35
			p.Stage = StageCooling
		case SignalRate:
			p.HealthScore -= 12
		default:
			p.HealthScore -= 4
		}
		if p.HealthScore < 0 {
			p.HealthScore = 0
		}
		if hold := circuitHold(sig, p.ConsecutiveErrors); hold > 0 {
			until := now.Add(hold)
			p.CircuitOpenUntil = &until
			e.log.Printf("protect: circuit open on %s for %s (%s)", sessionID, hold, errMsg)
		}
		if p.HealthScore < 25 && p.Stage != StagePaused {
			p.Stage = StagePaused
			e.log.Printf("protect: auto-paused %s (health %d)", sessionID, p.HealthScore)
		}
	}
	if err := e.store.UpsertProfile(ctx, p); err != nil {
		e.log.Printf("protect: save profile %s: %v", sessionID, err)
	}
}

// Snapshots returns every profile enriched with live hourly counts.
func (e *Engine) Snapshots(ctx context.Context) ([]Snapshot, error) {
	now := e.clock.Now()
	profiles, err := e.store.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	connected := map[string]bool{}
	if e.source != nil {
		if sessions, err := e.source.List(ctx); err == nil {
			for _, s := range sessions {
				connected[s.ID] = s.Status == domain.WhatsAppConnected
			}
		}
	}
	out := make([]Snapshot, 0, len(profiles))
	for i := range profiles {
		p := profiles[i]
		e.refreshDay(&p, now)
		hourly, _ := e.store.CountSuccessSince(ctx, p.SessionID, now.Add(-time.Hour))
		p.HourlySent = hourly
		p.RemainingToday = p.remaining()
		p.Connected = connected[p.SessionID]
		p.MatureSeed = e.isMaturePhone(p.PhoneNumber)
		p.Phase = PhaseForDay(p.WarmupDay)
		if p.Stage == StageMature {
			p.Phase = PhaseMature
		}
		p.PhaseLabel = PhaseLabel(p.Phase)
		p.CampaignBudget = CampaignBudget(p.WarmupDay, p.DailyCap)
		p.WarmupBudget = WarmupBudget(p.WarmupDay, p.DailyCap)
		p.CampaignSent = p.SentToday - p.WarmupSentToday
		if p.CampaignSent < 0 {
			p.CampaignSent = 0
		}
		p.StatusReason = e.statusReason(&p, now)
		if !p.Connected && strings.HasPrefix(p.SessionID, seedPrefix) {
			p.StatusReason = "aguardando pareamento do número"
		}
		out = append(out, Snapshot{
			Profile:   p,
			HourlyCap: HourlyCapFor(p.DailyCap, e.cfg.hours().businessSpan()),
		})
	}
	return out, nil
}

func (e *Engine) statusReason(p *Profile, now time.Time) string {
	if p.Stage == StagePaused {
		return "pausado"
	}
	if p.CircuitOpenUntil != nil && now.Before(*p.CircuitOpenUntil) {
		return "circuito aberto"
	}
	if p.SentToday >= p.DailyCap {
		return "teto diário atingido"
	}
	ok, _ := e.cfg.hours().inWindow(now)
	if !ok {
		return "fora da janela"
	}
	if p.Stage == StageWarming {
		return fmt.Sprintf("%s — dia %d de %d", PhaseLabel(PhaseForDay(p.WarmupDay)), p.WarmupDay, WarmupDays)
	}
	if p.Stage == StageCooling {
		return "resfriando após restrição"
	}
	return "maduro — ritmo de 200/dia"
}

// Pause / Resume are manual overrides from the dashboard.
func (e *Engine) Pause(ctx context.Context, sessionID string) error {
	p, err := e.store.GetProfile(ctx, sessionID)
	if err != nil {
		return err
	}
	if p == nil {
		return ErrProfileNotFound
	}
	p.Stage = StagePaused
	return e.store.UpsertProfile(ctx, p)
}

func (e *Engine) Resume(ctx context.Context, sessionID string) error {
	p, err := e.store.GetProfile(ctx, sessionID)
	if err != nil {
		return err
	}
	if p == nil {
		return ErrProfileNotFound
	}
	now := e.clock.Now()
	if e.isMaturePhone(p.PhoneNumber) {
		e.promoteMature(p, now)
	} else if p.WarmupDay >= WarmupDays {
		p.Stage = StageMature
	} else {
		p.Stage = StageWarming
	}
	p.CircuitOpenUntil = nil
	p.ConsecutiveErrors = 0
	if p.HealthScore < 50 {
		p.HealthScore = 50
	}
	e.refreshDay(p, now)
	return e.store.UpsertProfile(ctx, p)
}

func (e *Engine) ListContacts(ctx context.Context) ([]Contact, error) {
	return e.store.ListContacts(ctx)
}

func (e *Engine) AddContact(ctx context.Context, phone, label string) (*Contact, error) {
	return e.store.AddContact(ctx, phone, label)
}

func (e *Engine) DeleteContact(ctx context.Context, id string) error {
	return e.store.DeleteContact(ctx, id)
}

func (e *Engine) runWarmupPass() {
	if e.sender == nil {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, 40*time.Second)
	defer cancel()

	contacts, err := e.store.ListContacts(ctx)
	if err != nil {
		e.log.Printf("protect: list warmup contacts: %v", err)
		return
	}
	active := make([]Contact, 0, len(contacts))
	for _, c := range contacts {
		if c.Active {
			active = append(active, c)
		}
	}
	if len(active) == 0 {
		return
	}

	snaps, err := e.Snapshots(ctx)
	if err != nil {
		e.log.Printf("protect: snapshots for warmup: %v", err)
		return
	}

	now := e.clock.Now()
	for _, snap := range snaps {
		p := snap.Profile
		if !p.Connected || strings.HasPrefix(p.SessionID, seedPrefix) {
			continue
		}
		if p.Stage == StagePaused || p.Stage == StageCooling {
			continue
		}
		if !e.sender.SessionReady(p.SessionID) {
			e.log.Printf("protect: warmup skip %s (session not ready)", p.PhoneNumber)
			continue
		}
		dec, err := e.evaluate(ctx, p.SessionID, KindWarmup)
		if err != nil || !dec.Allow {
			e.log.Printf("protect: warmup skip %s (allow=%v wait=%s err=%v reason=%s)", p.PhoneNumber, dec.Allow, dec.Wait, err, dec.Reason)
			continue
		}
		// A number never warms up against itself: drop contacts whose phone
		// equals the sender's own number.
		pool := make([]Contact, 0, len(active))
		for _, c := range active {
			if digitsOnly(c.Phone) != p.PhoneNumber {
				pool = append(pool, c)
			}
		}
		contact := nextContact(pool, now, e.cfg.WarmupInterval)
		if contact == nil {
			e.log.Printf("protect: warmup skip %s (no eligible contact, pool %d)", p.PhoneNumber, len(pool))
			continue // only contact is on cooldown; retry on a later tick
		}
		e.mu.Lock()
		last := e.lastWarm[p.SessionID]
		phrase := pickWarmupPhrase(now, last, p.WarmupDay)
		e.lastWarm[p.SessionID] = phrase
		e.mu.Unlock()

		sendCtx, sendCancel := context.WithTimeout(ctx, 20*time.Second)
		err = e.sender.SendText(sendCtx, p.SessionID, contact.Phone, phrase)
		sendCancel()
		if err != nil {
			e.record(ctx, p.SessionID, KindWarmup, contact.Phone, false, err.Error())
			e.log.Printf("protect: warmup send on %s failed: %v", p.SessionID, err)
			continue
		}
		e.record(ctx, p.SessionID, KindWarmup, contact.Phone, true, "")
		_ = e.store.TouchContact(ctx, contact.ID)
		e.log.Printf("protect: warmup ping %s → %s", p.PhoneNumber, contact.Phone)
		// One ping per pass per number keeps the worker gentle.
	}
}

func nextContact(contacts []Contact, now time.Time, minGap time.Duration) *Contact {
	var best *Contact
	for i := range contacts {
		c := &contacts[i]
		if c.LastSentAt != nil && now.Sub(*c.LastSentAt) < minGap {
			continue
		}
		if best == nil {
			best = c
			continue
		}
		if c.LastSentAt == nil {
			best = c
			continue
		}
		if best.LastSentAt != nil && c.LastSentAt.Before(*best.LastSentAt) {
			best = c
		}
	}
	return best
}

// RenderMessage is used by the dispatcher so campaign bodies get saudação/jitter.
func (e *Engine) RenderMessage(body string) string {
	return Render(body, e.clock.Now())
}
