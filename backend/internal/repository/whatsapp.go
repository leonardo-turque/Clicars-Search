package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zennitex/clicars-search/internal/domain"
)

// WhatsAppRepository persists WhatsApp session metadata in PostgreSQL. The
// encrypted device material itself is stored separately by whatsmeow's own
// sqlstore; this repository only tracks the app-level view (id, number, status).
type WhatsAppRepository struct {
	pool *pgxpool.Pool
}

func NewWhatsAppRepository(pool *pgxpool.Pool) *WhatsAppRepository {
	return &WhatsAppRepository{pool: pool}
}

// sessionColumns is the canonical projection shared by every read, flattening
// the JID out of the session_data JSONB blob.
const sessionColumns = `id::text, COALESCE(session_data->>'jid', ''), COALESCE(phone_number, ''), status, created_at`

// Insert creates a session row using a caller-supplied id, so the in-memory
// manager and the database share a single identifier from the moment a pairing
// succeeds. The JID is stored inside session_data as the pointer to whatsmeow's
// own device record.
func (r *WhatsAppRepository) Insert(ctx context.Context, id, jid, phone, status string) (*domain.WhatsAppSession, error) {
	data, err := json.Marshal(map[string]string{"jid": jid})
	if err != nil {
		return nil, fmt.Errorf("marshal session_data: %w", err)
	}

	var s domain.WhatsAppSession
	err = r.pool.QueryRow(ctx,
		`INSERT INTO whatsapp_sessions (id, session_data, phone_number, status)
		 VALUES ($1::uuid, $2::jsonb, $3, $4)
		 RETURNING `+sessionColumns,
		id, data, nullIfEmpty(phone), status,
	).Scan(&s.ID, &s.JID, &s.PhoneNumber, &s.Status, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert whatsapp session: %w", err)
	}
	return &s, nil
}

// Update refreshes the JID, phone number and status of an existing session.
// Returns domain.ErrNotFound when no row matches the id.
func (r *WhatsAppRepository) Update(ctx context.Context, id, jid, phone, status string) error {
	data, err := json.Marshal(map[string]string{"jid": jid})
	if err != nil {
		return fmt.Errorf("marshal session_data: %w", err)
	}

	tag, err := r.pool.Exec(ctx,
		`UPDATE whatsapp_sessions
		    SET session_data = $2::jsonb, phone_number = $3, status = $4
		  WHERE id = $1::uuid`,
		id, data, nullIfEmpty(phone), status,
	)
	if err != nil {
		return fmt.Errorf("update whatsapp session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UpdateStatus flips only the status column (e.g. CONNECTED -> DISCONNECTED on a
// dropped link). Returns domain.ErrNotFound when no row matches the id.
func (r *WhatsAppRepository) UpdateStatus(ctx context.Context, id, status string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE whatsapp_sessions SET status = $2 WHERE id = $1::uuid`,
		id, status,
	)
	if err != nil {
		return fmt.Errorf("update whatsapp session status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// List returns every session ordered oldest-first, which keeps slot positions
// stable in the frontend grid across refreshes.
func (r *WhatsAppRepository) List(ctx context.Context) ([]domain.WhatsAppSession, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+sessionColumns+` FROM whatsapp_sessions ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("query whatsapp sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]domain.WhatsAppSession, 0)
	for rows.Next() {
		var s domain.WhatsAppSession
		if err := rows.Scan(&s.ID, &s.JID, &s.PhoneNumber, &s.Status, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan whatsapp session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// GetByID returns a single session or domain.ErrNotFound.
func (r *WhatsAppRepository) GetByID(ctx context.Context, id string) (*domain.WhatsAppSession, error) {
	var s domain.WhatsAppSession
	err := r.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM whatsapp_sessions WHERE id = $1::uuid`,
		id,
	).Scan(&s.ID, &s.JID, &s.PhoneNumber, &s.Status, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("query whatsapp session: %w", err)
	}
	return &s, nil
}

// Delete removes a session row. Returns domain.ErrNotFound when absent.
func (r *WhatsAppRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM whatsapp_sessions WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("delete whatsapp session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteOldDisconnected enforces the 45-day retention policy: it removes only
// sessions that are DISCONNECTED and older than `days`, so a live number is
// never reaped out from under an active connection. Returns the rows removed.
func (r *WhatsAppRepository) DeleteOldDisconnected(ctx context.Context, days int) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM whatsapp_sessions
		  WHERE status = 'DISCONNECTED'
		    AND created_at < NOW() - ($1::integer * INTERVAL '1 day')`,
		days,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old whatsapp sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// nullIfEmpty maps an empty string to a SQL NULL so phone_number stays NULL
// until a pairing actually reveals the number.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
