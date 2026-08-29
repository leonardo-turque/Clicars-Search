package protect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists number profiles, send events and warmup contacts.
type Store interface {
	EnsureSchema(ctx context.Context) error
	GetProfile(ctx context.Context, sessionID string) (*Profile, error)
	GetProfileByPhone(ctx context.Context, phone string) (*Profile, error)
	ListProfiles(ctx context.Context) ([]Profile, error)
	UpsertProfile(ctx context.Context, p *Profile) error
	RebindSession(ctx context.Context, oldSessionID, newSessionID string) error
	InsertEvent(ctx context.Context, e Event) error
	CountSuccessSince(ctx context.Context, sessionID string, since time.Time) (int, error)
	CountKindToday(ctx context.Context, sessionID, kind string, dayStart time.Time) (int, error)
	ListContacts(ctx context.Context) ([]Contact, error)
	AddContact(ctx context.Context, phone, label string) (*Contact, error)
	DeleteContact(ctx context.Context, id string) error
	TouchContact(ctx context.Context, id string) error
}

type pgStore struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) Store {
	return &pgStore{pool: pool}
}

func (s *pgStore) EnsureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS whatsapp_number_profiles (
		    session_id          TEXT         PRIMARY KEY,
		    phone_number        VARCHAR(30)  NOT NULL DEFAULT '',
		    stage               VARCHAR(20)  NOT NULL DEFAULT 'WARMING'
		                        CHECK (stage IN ('WARMING', 'MATURE', 'PAUSED', 'COOLING')),
		    warmup_started_at   TIMESTAMP    NOT NULL DEFAULT NOW(),
		    warmup_day          INT          NOT NULL DEFAULT 1,
		    daily_cap           INT          NOT NULL DEFAULT 8,
		    target_daily        INT          NOT NULL DEFAULT 200,
		    health_score        INT          NOT NULL DEFAULT 70,
		    consecutive_errors  INT          NOT NULL DEFAULT 0,
		    circuit_open_until  TIMESTAMP,
		    last_error          TEXT,
		    last_sent_at        TIMESTAMP,
		    sent_today          INT          NOT NULL DEFAULT 0,
		    sent_today_date     DATE         NOT NULL DEFAULT CURRENT_DATE,
		    burst_count         INT          NOT NULL DEFAULT 0,
		    warmup_sent_today   INT          NOT NULL DEFAULT 0,
		    created_at          TIMESTAMP    NOT NULL DEFAULT NOW(),
		    updated_at          TIMESTAMP    NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_wa_profiles_phone
		    ON whatsapp_number_profiles (phone_number);

		CREATE TABLE IF NOT EXISTS whatsapp_send_events (
		    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
		    session_id  TEXT         NOT NULL,
		    kind        VARCHAR(20)  NOT NULL DEFAULT 'CAMPAIGN'
		                CHECK (kind IN ('CAMPAIGN', 'WARMUP')),
		    phone       VARCHAR(30),
		    success     BOOLEAN      NOT NULL,
		    error       TEXT,
		    created_at  TIMESTAMP    NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_wa_send_events_session_time
		    ON whatsapp_send_events (session_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS whatsapp_warmup_contacts (
		    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
		    phone        VARCHAR(30)  NOT NULL UNIQUE,
		    label        TEXT,
		    active       BOOLEAN      NOT NULL DEFAULT TRUE,
		    last_sent_at TIMESTAMP,
		    created_at   TIMESTAMP    NOT NULL DEFAULT NOW()
		);
	`)
	if err != nil {
		return fmt.Errorf("ensure protect schema: %w", err)
	}
	return nil
}

const profileCols = `session_id, phone_number, stage, warmup_started_at, warmup_day,
	daily_cap, target_daily, health_score, consecutive_errors, circuit_open_until,
	last_error, last_sent_at, sent_today, sent_today_date, burst_count,
	warmup_sent_today, created_at, updated_at`

func scanProfile(row interface{ Scan(dest ...any) error }) (*Profile, error) {
	var p Profile
	var lastErr *string
	err := row.Scan(
		&p.SessionID, &p.PhoneNumber, &p.Stage, &p.WarmupStartedAt, &p.WarmupDay,
		&p.DailyCap, &p.TargetDaily, &p.HealthScore, &p.ConsecutiveErrors, &p.CircuitOpenUntil,
		&lastErr, &p.LastSentAt, &p.SentToday, &p.SentTodayDate, &p.BurstCount,
		&p.WarmupSentToday, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if lastErr != nil {
		p.LastError = *lastErr
	}
	p.RemainingToday = p.remaining()
	return &p, nil
}

func (s *pgStore) GetProfile(ctx context.Context, sessionID string) (*Profile, error) {
	p, err := scanProfile(s.pool.QueryRow(ctx,
		`SELECT `+profileCols+` FROM whatsapp_number_profiles WHERE session_id = $1`, sessionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get profile: %w", err)
	}
	return p, nil
}

func (s *pgStore) GetProfileByPhone(ctx context.Context, phone string) (*Profile, error) {
	phone = digitsOnly(phone)
	if phone == "" {
		return nil, nil
	}
	p, err := scanProfile(s.pool.QueryRow(ctx,
		`SELECT `+profileCols+` FROM whatsapp_number_profiles WHERE phone_number = $1
		 ORDER BY updated_at DESC LIMIT 1`, phone))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get profile by phone: %w", err)
	}
	return p, nil
}

func (s *pgStore) ListProfiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+profileCols+` FROM whatsapp_number_profiles ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *pgStore) UpsertProfile(ctx context.Context, p *Profile) error {
	if p.SessionID == "" {
		return fmt.Errorf("upsert profile: empty session id")
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	_, err := s.pool.Exec(ctx, `
		INSERT INTO whatsapp_number_profiles (
		    session_id, phone_number, stage, warmup_started_at, warmup_day,
		    daily_cap, target_daily, health_score, consecutive_errors, circuit_open_until,
		    last_error, last_sent_at, sent_today, sent_today_date, burst_count,
		    warmup_sent_today, created_at, updated_at
		) VALUES (
		    $1,$2,$3,$4,$5,
		    $6,$7,$8,$9,$10,
		    NULLIF($11,''),$12,$13,$14,$15,
		    $16,$17,$18
		)
		ON CONFLICT (session_id) DO UPDATE SET
		    phone_number        = EXCLUDED.phone_number,
		    stage               = EXCLUDED.stage,
		    warmup_started_at   = EXCLUDED.warmup_started_at,
		    warmup_day          = EXCLUDED.warmup_day,
		    daily_cap           = EXCLUDED.daily_cap,
		    target_daily        = EXCLUDED.target_daily,
		    health_score        = EXCLUDED.health_score,
		    consecutive_errors  = EXCLUDED.consecutive_errors,
		    circuit_open_until  = EXCLUDED.circuit_open_until,
		    last_error          = EXCLUDED.last_error,
		    last_sent_at        = EXCLUDED.last_sent_at,
		    sent_today          = EXCLUDED.sent_today,
		    sent_today_date     = EXCLUDED.sent_today_date,
		    burst_count         = EXCLUDED.burst_count,
		    warmup_sent_today   = EXCLUDED.warmup_sent_today,
		    updated_at          = EXCLUDED.updated_at`,
		p.SessionID, p.PhoneNumber, p.Stage, p.WarmupStartedAt, p.WarmupDay,
		p.DailyCap, p.TargetDaily, p.HealthScore, p.ConsecutiveErrors, p.CircuitOpenUntil,
		p.LastError, p.LastSentAt, p.SentToday, p.SentTodayDate, p.BurstCount,
		p.WarmupSentToday, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert profile: %w", err)
	}
	return nil
}

func (s *pgStore) RebindSession(ctx context.Context, oldSessionID, newSessionID string) error {
	if oldSessionID == "" || newSessionID == "" || oldSessionID == newSessionID {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE whatsapp_number_profiles SET session_id = $2, updated_at = NOW()
		 WHERE session_id = $1`, oldSessionID, newSessionID)
	if err != nil {
		return fmt.Errorf("rebind session: %w", err)
	}
	return nil
}

func (s *pgStore) InsertEvent(ctx context.Context, e Event) error {
	if e.Kind == "" {
		e.Kind = KindCampaign
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO whatsapp_send_events (session_id, kind, phone, success, error)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))`,
		e.SessionID, e.Kind, e.Phone, e.Success, e.Error)
	if err != nil {
		return fmt.Errorf("insert send event: %w", err)
	}
	return nil
}

func (s *pgStore) CountSuccessSince(ctx context.Context, sessionID string, since time.Time) (int, error) {
	// created_at is a naive TIMESTAMP stored in UTC; normalize the cutoff so
	// the driver does not bind a local-wall-clock value (off-by-timezone bug).
	since = since.UTC()
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM whatsapp_send_events
		 WHERE session_id = $1 AND success = TRUE AND created_at >= $2`,
		sessionID, since,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count success since: %w", err)
	}
	return n, nil
}

func (s *pgStore) CountKindToday(ctx context.Context, sessionID, kind string, dayStart time.Time) (int, error) {
	// Same naive-UTC column as CountSuccessSince.
	dayStart = dayStart.UTC()
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM whatsapp_send_events
		 WHERE session_id = $1 AND kind = $2 AND success = TRUE AND created_at >= $3`,
		sessionID, kind, dayStart,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count kind today: %w", err)
	}
	return n, nil
}

func (s *pgStore) ListContacts(ctx context.Context) ([]Contact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, phone, COALESCE(label,''), active, last_sent_at, created_at
		  FROM whatsapp_warmup_contacts
		 ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list warmup contacts: %w", err)
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Phone, &c.Label, &c.Active, &c.LastSentAt, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan warmup contact: %w", err)
		}
		out = append(out, c)
	}
	if out == nil {
		out = []Contact{}
	}
	return out, rows.Err()
}

func (s *pgStore) AddContact(ctx context.Context, phone, label string) (*Contact, error) {
	phone = digitsOnly(phone)
	if !looksLikePhone(phone) {
		return nil, ErrInvalidContact
	}
	var c Contact
	err := s.pool.QueryRow(ctx, `
		INSERT INTO whatsapp_warmup_contacts (phone, label, active)
		VALUES ($1, NULLIF($2,''), TRUE)
		ON CONFLICT (phone) DO UPDATE SET active = TRUE, label = COALESCE(NULLIF($2,''), whatsapp_warmup_contacts.label)
		RETURNING id::text, phone, COALESCE(label,''), active, last_sent_at, created_at`,
		phone, strings.TrimSpace(label),
	).Scan(&c.ID, &c.Phone, &c.Label, &c.Active, &c.LastSentAt, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("add warmup contact: %w", err)
	}
	return &c, nil
}

func (s *pgStore) DeleteContact(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM whatsapp_warmup_contacts WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("delete warmup contact: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrContactNotFound
	}
	return nil
}

func (s *pgStore) TouchContact(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE whatsapp_warmup_contacts SET last_sent_at = NOW() WHERE id = $1::uuid`, id)
	return err
}

var (
	ErrInvalidContact  = errors.New("invalid warmup contact phone")
	ErrContactNotFound = errors.New("warmup contact not found")
)
