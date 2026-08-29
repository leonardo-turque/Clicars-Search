package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zennitex/clicars-search/internal/domain"
)

// CampaignRepository persists messaging campaigns, their durable per-recipient
// queue, and the lead phone numbers a campaign targets. It implements the
// campaign.Store and campaign.LeadSource ports.
type CampaignRepository struct {
	pool *pgxpool.Pool
}

func NewCampaignRepository(pool *pgxpool.Pool) *CampaignRepository {
	return &CampaignRepository{pool: pool}
}

// EnsureSchema creates the durable campaign_messages queue (and related indexes)
// if they are missing. Safe to call on every boot — needed because docker's
// init.sql only runs on a fresh Postgres volume.
func (r *CampaignRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS campaign_messages (
		    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
		    campaign_id  UUID         NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
		    phone        VARCHAR(30)  NOT NULL,
		    status       VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
		                 CHECK (status IN ('PENDING', 'SENDING', 'SENT', 'FAILED', 'SKIPPED')),
		    attempts     INT          NOT NULL DEFAULT 0,
		    last_error   TEXT,
		    scheduled_at TIMESTAMP    NOT NULL DEFAULT NOW(),
		    sent_at      TIMESTAMP,
		    created_at   TIMESTAMP    NOT NULL DEFAULT NOW(),
		    UNIQUE (campaign_id, phone)
		);
		CREATE INDEX IF NOT EXISTS idx_campaign_messages_claim
		    ON campaign_messages (status, scheduled_at)
		    WHERE status = 'PENDING';
		CREATE INDEX IF NOT EXISTS idx_campaign_messages_campaign
		    ON campaign_messages (campaign_id, status);
		CREATE INDEX IF NOT EXISTS idx_campaigns_session_status
		    ON campaigns (whatsapp_session_id, status);
		ALTER TABLE campaigns
		    ADD COLUMN IF NOT EXISTS consent_confirmed BOOLEAN NOT NULL DEFAULT FALSE;
	`)
	if err != nil {
		return fmt.Errorf("ensure campaign schema: %w", err)
	}
	return nil
}

// campaignColumns is the canonical projection shared by every campaign read.
const campaignColumns = `id::text, search_id::text, whatsapp_session_id::text,
	message_body, consent_confirmed, status, total_count, sent_count, failed_count, created_at`

// Create inserts a new campaign row and writes the generated id/created_at back
// onto the entity. The caller supplies SearchID, WhatsAppSessionID, MessageBody,
// Status and Total.
func (r *CampaignRepository) Create(ctx context.Context, c *domain.Campaign) error {
	err := r.pool.QueryRow(ctx,
		`INSERT INTO campaigns
		     (search_id, whatsapp_session_id, message_body, consent_confirmed, status, total_count)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
		 RETURNING id::text, created_at`,
		c.SearchID, c.WhatsAppSessionID, c.MessageBody, c.ConsentConfirmed, c.Status, c.Total,
	).Scan(&c.ID, &c.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert campaign: %w", err)
	}
	return nil
}

// EnqueueMessages snapshots the normalised phone list into campaign_messages as
// PENDING rows. Inserts in chunks so 100k+ leads stay practical without a huge
// single parameter array.
func (r *CampaignRepository) EnqueueMessages(ctx context.Context, campaignID string, phones []string) error {
	const chunkSize = 5000
	for start := 0; start < len(phones); start += chunkSize {
		end := start + chunkSize
		if end > len(phones) {
			end = len(phones)
		}
		chunk := phones[start:end]
		_, err := r.pool.Exec(ctx, `
			INSERT INTO campaign_messages (campaign_id, phone, status, scheduled_at)
			SELECT $1::uuid, p, 'PENDING', NOW()
			  FROM unnest($2::text[]) AS p
			ON CONFLICT (campaign_id, phone) DO NOTHING`,
			campaignID, chunk,
		)
		if err != nil {
			return fmt.Errorf("enqueue campaign messages: %w", err)
		}
	}
	return nil
}

// UpdateStatus flips the status column (PENDING → RUNNING → COMPLETED).
// Returns domain.ErrNotFound when no row matches the id.
func (r *CampaignRepository) UpdateStatus(ctx context.Context, id, status string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE campaigns SET status = $2 WHERE id = $1::uuid`,
		id, status,
	)
	if err != nil {
		return fmt.Errorf("update campaign status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetProgress records the running tally after each message attempt so GET
// /campaigns/{id} can report live progress. Returns domain.ErrNotFound when no
// row matches the id.
func (r *CampaignRepository) SetProgress(ctx context.Context, id string, sent, failed int) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE campaigns SET sent_count = $2, failed_count = $3 WHERE id = $1::uuid`,
		id, sent, failed,
	)
	if err != nil {
		return fmt.Errorf("update campaign progress: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// BumpProgress atomically increments sent_count and/or failed_count. Prefer this
// over SetProgress when multiple workers may touch the same campaign.
func (r *CampaignRepository) BumpProgress(ctx context.Context, id string, sentDelta, failedDelta int) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE campaigns
		    SET sent_count   = sent_count   + $2,
		        failed_count = failed_count + $3
		  WHERE id = $1::uuid`,
		id, sentDelta, failedDelta,
	)
	if err != nil {
		return fmt.Errorf("bump campaign progress: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetByID returns a single campaign or domain.ErrNotFound.
func (r *CampaignRepository) GetByID(ctx context.Context, id string) (*domain.Campaign, error) {
	var c domain.Campaign
	err := r.pool.QueryRow(ctx,
		`SELECT `+campaignColumns+` FROM campaigns WHERE id = $1::uuid`,
		id,
	).Scan(&c.ID, &c.SearchID, &c.WhatsAppSessionID, &c.MessageBody,
		&c.ConsentConfirmed, &c.Status, &c.Total, &c.Sent, &c.Failed, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("query campaign: %w", err)
	}
	return &c, nil
}

// ListAll returns the most recent campaigns enriched with their search niche/location
// and the sender's phone number (empty if the session was deleted).
func (r *CampaignRepository) ListAll(ctx context.Context, limit int) ([]domain.CampaignSummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			c.id::text, c.search_id::text, c.whatsapp_session_id::text,
			c.message_body, c.consent_confirmed, c.status, c.total_count, c.sent_count, c.failed_count, c.created_at,
			s.niche, s.location,
			COALESCE(ws.phone_number, '') AS phone_number
		FROM campaigns c
		JOIN searches s ON s.id = c.search_id
		LEFT JOIN whatsapp_sessions ws ON ws.id = c.whatsapp_session_id
		ORDER BY c.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	defer rows.Close()

	var out []domain.CampaignSummary
	for rows.Next() {
		var cs domain.CampaignSummary
		if err := rows.Scan(
			&cs.ID, &cs.SearchID, &cs.WhatsAppSessionID,
			&cs.MessageBody, &cs.ConsentConfirmed, &cs.Status, &cs.Total, &cs.Sent, &cs.Failed, &cs.CreatedAt,
			&cs.SearchNiche, &cs.SearchLocation, &cs.PhoneNumber,
		); err != nil {
			return nil, fmt.Errorf("scan campaign summary: %w", err)
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// ClaimNext atomically picks the next due PENDING message for any incomplete
// campaign on the given WhatsApp session, marks it SENDING, and returns it with
// denormalised campaign fields. Returns (nil, nil) when the queue is empty.
func (r *CampaignRepository) ClaimNext(ctx context.Context, sessionID string) (*domain.CampaignMessage, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var m domain.CampaignMessage
	err = tx.QueryRow(ctx, `
		SELECT m.id::text, m.campaign_id::text, m.phone, m.attempts,
		       c.whatsapp_session_id::text, c.message_body
		  FROM campaign_messages m
		  JOIN campaigns c ON c.id = m.campaign_id
		 WHERE m.status = 'PENDING'
		   AND m.scheduled_at <= NOW()
		   AND c.whatsapp_session_id = $1::uuid
		   AND c.status IN ('PENDING', 'RUNNING')
		 ORDER BY m.scheduled_at ASC
		 LIMIT 1
		 FOR UPDATE OF m SKIP LOCKED`,
		sessionID,
	).Scan(&m.ID, &m.CampaignID, &m.Phone, &m.Attempts,
		&m.WhatsAppSessionID, &m.MessageBody)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claim next message: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE campaign_messages
		   SET status = 'SENDING', attempts = attempts + 1
		 WHERE id = $1::uuid`, m.ID)
	if err != nil {
		return nil, fmt.Errorf("mark sending: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, nil
	}

	// Flip campaign to RUNNING on first claim (idempotent if already RUNNING).
	if _, err := tx.Exec(ctx, `
		UPDATE campaigns SET status = 'RUNNING'
		 WHERE id = $1::uuid AND status = 'PENDING'`, m.CampaignID); err != nil {
		return nil, fmt.Errorf("mark campaign running: %w", err)
	}

	m.Status = domain.MessageSending
	m.Attempts++
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return &m, nil
}

// MarkMessage sets a terminal status on a claimed row and optionally records
// last_error / sent_at.
func (r *CampaignRepository) MarkMessage(ctx context.Context, id, status, lastError string) error {
	var err error
	switch status {
	case domain.MessageSent:
		_, err = r.pool.Exec(ctx, `
			UPDATE campaign_messages
			   SET status = $2, last_error = NULL, sent_at = NOW()
			 WHERE id = $1::uuid`, id, status)
	case domain.MessageFailed, domain.MessageSkipped:
		_, err = r.pool.Exec(ctx, `
			UPDATE campaign_messages
			   SET status = $2, last_error = NULLIF($3, ''), sent_at = NULL
			 WHERE id = $1::uuid`, id, status, lastError)
	case domain.MessagePending:
		// Re-queue (e.g. session offline): clear SENDING without counting as attempt success.
		_, err = r.pool.Exec(ctx, `
			UPDATE campaign_messages
			   SET status = 'PENDING', last_error = NULLIF($2, '')
			 WHERE id = $1::uuid`, id, lastError)
	default:
		return fmt.Errorf("mark message: invalid status %q", status)
	}
	if err != nil {
		return fmt.Errorf("mark message: %w", err)
	}
	return nil
}

// RescheduleMessage returns a message to PENDING with a future scheduled_at
// (used when the WhatsApp session is temporarily offline).
func (r *CampaignRepository) RescheduleMessage(ctx context.Context, id string, at time.Time, reason string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE campaign_messages
		   SET status = 'PENDING',
		       scheduled_at = $2,
		       last_error = NULLIF($3, '')
		 WHERE id = $1::uuid`, id, at, reason)
	if err != nil {
		return fmt.Errorf("reschedule message: %w", err)
	}
	return nil
}

// ReclaimAllSending resets every SENDING row to PENDING. Intended for startup
// resume when no other worker process can still be holding a claim.
func (r *CampaignRepository) ReclaimAllSending(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE campaign_messages
		   SET status = 'PENDING',
		       scheduled_at = LEAST(scheduled_at, NOW()),
		       last_error = 'reclaimed on startup'
		 WHERE status = 'SENDING'`)
	if err != nil {
		return 0, fmt.Errorf("reclaim all sending: %w", err)
	}
	return tag.RowsAffected(), nil
}

// HasPending reports whether the campaign still has unfinished queue work.
func (r *CampaignRepository) HasPending(ctx context.Context, campaignID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM campaign_messages
			 WHERE campaign_id = $1::uuid
			   AND status IN ('PENDING', 'SENDING')
		)`, campaignID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has pending: %w", err)
	}
	return exists, nil
}

// ListSessionsWithWork returns distinct WhatsApp session IDs that still have
// PENDING (or SENDING) messages on incomplete campaigns — used to wake workers
// on startup and after enqueue.
func (r *CampaignRepository) ListSessionsWithWork(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT c.whatsapp_session_id::text
		  FROM campaigns c
		  JOIN campaign_messages m ON m.campaign_id = c.id
		 WHERE c.status IN ('PENDING', 'RUNNING')
		   AND m.status IN ('PENDING', 'SENDING')`)
	if err != nil {
		return nil, fmt.Errorf("list sessions with work: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// NextScheduledAt returns the earliest scheduled_at among PENDING messages for
// the session, or zero time if none.
func (r *CampaignRepository) NextScheduledAt(ctx context.Context, sessionID string) (time.Time, error) {
	var at *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT MIN(m.scheduled_at)
		  FROM campaign_messages m
		  JOIN campaigns c ON c.id = m.campaign_id
		 WHERE m.status = 'PENDING'
		   AND c.whatsapp_session_id = $1::uuid
		   AND c.status IN ('PENDING', 'RUNNING')`,
		sessionID,
	).Scan(&at)
	if err != nil {
		return time.Time{}, fmt.Errorf("next scheduled: %w", err)
	}
	if at == nil {
		return time.Time{}, nil
	}
	return *at, nil
}

// GetPhonesBySearchID returns the non-empty phone numbers of every company under
// a search — the raw lead list a campaign dispatches to. Normalisation and
// de-duplication happen in the dispatcher; this just filters out blanks.
func (r *CampaignRepository) GetPhonesBySearchID(ctx context.Context, searchID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT phone FROM companies
		  WHERE search_id = $1::uuid AND phone IS NOT NULL AND phone <> ''
		  ORDER BY created_at`,
		searchID,
	)
	if err != nil {
		return nil, fmt.Errorf("query campaign phones: %w", err)
	}
	defer rows.Close()

	phones := make([]string, 0)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan phone: %w", err)
		}
		phones = append(phones, p)
	}
	return phones, rows.Err()
}

// DeleteOldCampaigns enforces the 45-day retention policy on completed campaigns
// only, so in-flight multi-week blasts are never wiped by age.
func (r *CampaignRepository) DeleteOldCampaigns(ctx context.Context, days int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM campaigns c
		 WHERE c.created_at < NOW() - ($1::integer * INTERVAL '1 day')
		   AND c.status = 'COMPLETED'
		   AND NOT EXISTS (
		       SELECT 1 FROM campaign_messages m
		        WHERE m.campaign_id = c.id
		          AND m.status IN ('PENDING', 'SENDING')
		   )`,
		days,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old campaigns: %w", err)
	}
	return tag.RowsAffected(), nil
}
