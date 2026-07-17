package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zennitex/clicars-search/internal/domain"
)

// SearchRepository persists searches and their companies in PostgreSQL.
// It implements usecase.AsyncSearchStore and usecase.HistoryReader.
type SearchRepository struct {
	pool *pgxpool.Pool
}

func NewSearchRepository(pool *pgxpool.Pool) *SearchRepository {
	return &SearchRepository{pool: pool}
}

// ── Async lifecycle ─────────────────────────────────────────────────────────

// CreateSearch inserts a search row with status PENDING and writes back
// the generated id and created_at.
func (r *SearchRepository) CreateSearch(ctx context.Context, search *domain.Search) error {
	return r.pool.QueryRow(ctx,
		`INSERT INTO searches (niche, location, quantity, status)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text, created_at`,
		search.Niche, search.Location, search.Quantity, domain.SearchStatusPending,
	).Scan(&search.ID, &search.CreatedAt)
}

// SetSearchRunning transitions the search to RUNNING and records started_at.
func (r *SearchRepository) SetSearchRunning(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE searches SET status = 'RUNNING', started_at = NOW() WHERE id = $1::uuid`,
		id,
	)
	return err
}

// UpdateSearchProgress writes the latest enriched-company count to the DB.
func (r *SearchRepository) UpdateSearchProgress(ctx context.Context, id string, progress int) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE searches SET progress = $2 WHERE id = $1::uuid`,
		id, progress,
	)
	return err
}

// SaveSearchCompanies bulk-inserts companies under the given search.
func (r *SearchRepository) SaveSearchCompanies(ctx context.Context, searchID string, companies []domain.Company) ([]domain.Company, error) {
	if len(companies) == 0 {
		return []domain.Company{}, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	for _, c := range companies {
		batch.Queue(
			`INSERT INTO companies (search_id, name, location, phone, website)
			 VALUES ($1::uuid, $2, $3, $4, $5)
			 RETURNING id::text, created_at`,
			searchID, c.Name, c.Location, c.Phone, c.Website,
		)
	}

	saved := make([]domain.Company, 0, len(companies))
	br := tx.SendBatch(ctx, batch)
	for _, c := range companies {
		c.SearchID = searchID
		if err := br.QueryRow().Scan(&c.ID, &c.CreatedAt); err != nil {
			br.Close()
			return nil, fmt.Errorf("insert company: %w", err)
		}
		saved = append(saved, c)
	}
	if err := br.Close(); err != nil {
		return nil, fmt.Errorf("close company batch: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return saved, nil
}

// SetSearchCompleted marks the search as COMPLETED with final progress count.
func (r *SearchRepository) SetSearchCompleted(ctx context.Context, id string, progress int) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE searches
		 SET status = 'COMPLETED', progress = $2, completed_at = NOW()
		 WHERE id = $1::uuid`,
		id, progress,
	)
	return err
}

// SetSearchFailed marks the search as FAILED and stores the error message.
func (r *SearchRepository) SetSearchFailed(ctx context.Context, id, msg string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE searches SET status = 'FAILED', error_msg = $2, completed_at = NOW() WHERE id = $1::uuid`,
		id, msg,
	)
	return err
}

// ── History queries ──────────────────────────────────────────────────────────

// GetSearches returns the most recent `limit` searches ordered newest first.
func (r *SearchRepository) GetSearches(ctx context.Context, limit int) ([]domain.Search, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, niche, location, quantity, status, progress, created_at
		 FROM searches
		 ORDER BY created_at DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query searches: %w", err)
	}
	defer rows.Close()

	searches := make([]domain.Search, 0)
	for rows.Next() {
		var s domain.Search
		if err := rows.Scan(&s.ID, &s.Niche, &s.Location, &s.Quantity, &s.Status, &s.Progress, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan search: %w", err)
		}
		searches = append(searches, s)
	}
	return searches, rows.Err()
}

// GetSearchByID returns a search and its companies. Returns domain.ErrNotFound
// if no search with the given id exists.
func (r *SearchRepository) GetSearchByID(ctx context.Context, id string) (*domain.Search, []domain.Company, error) {
	var s domain.Search
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, niche, location, quantity, status, progress,
		        COALESCE(error_msg, ''), started_at, completed_at, created_at
		 FROM searches WHERE id = $1::uuid`,
		id,
	).Scan(&s.ID, &s.Niche, &s.Location, &s.Quantity, &s.Status, &s.Progress,
		&s.ErrorMsg, &s.StartedAt, &s.CompletedAt, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, domain.ErrNotFound
		}
		return nil, nil, fmt.Errorf("query search: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT id::text, search_id::text, name, location, phone, website, created_at
		 FROM companies WHERE search_id = $1::uuid ORDER BY created_at`,
		id,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query companies: %w", err)
	}
	defer rows.Close()

	companies := make([]domain.Company, 0)
	for rows.Next() {
		var c domain.Company
		if err := rows.Scan(&c.ID, &c.SearchID, &c.Name, &c.Location, &c.Phone, &c.Website, &c.CreatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan company: %w", err)
		}
		companies = append(companies, c)
	}
	return &s, companies, rows.Err()
}

// DeleteOldSearches removes searches older than `days` days and returns the
// count of deleted rows. Companies are removed via ON DELETE CASCADE.
func (r *SearchRepository) DeleteOldSearches(ctx context.Context, days int) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM searches WHERE created_at < NOW() - ($1::integer * INTERVAL '1 day')`,
		days,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old searches: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── Legacy helper (used by integration tests) ────────────────────────────────

// SaveSearchWithCompanies inserts the search metadata and all companies inside a
// single transaction. Kept for integration test compatibility.
func (r *SearchRepository) SaveSearchWithCompanies(ctx context.Context, search *domain.Search, companies []domain.Company) ([]domain.Company, error) {
	search.Status = domain.SearchStatusCompleted
	if err := r.CreateSearch(ctx, search); err != nil {
		return nil, fmt.Errorf("insert search: %w", err)
	}
	saved, err := r.SaveSearchCompanies(ctx, search.ID, companies)
	if err != nil {
		return nil, err
	}
	if err := r.SetSearchCompleted(ctx, search.ID, len(saved)); err != nil {
		return nil, err
	}
	return saved, nil
}
