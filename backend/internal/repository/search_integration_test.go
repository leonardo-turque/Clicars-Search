//go:build integration

// Integration tests for the Postgres persistence layer. They require a running
// PostgreSQL with the schema from db/init.sql applied.
//
// Run with:
//
//	docker compose up -d db
//	go test -tags=integration ./internal/repository/...
//
// Override the connection with TEST_DATABASE_URL if needed.
package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zennitex/clicars-search/internal/domain"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://clicars:clicars_pass@localhost:5432/clicars_search"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	return pool
}

func TestSaveSearchWithCompanies_Integration(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewSearchRepository(pool)
	ctx := context.Background()

	search := &domain.Search{Niche: "integration-niche", Location: "Test City", Quantity: 2}
	companies := []domain.Company{
		{Name: "Co A", Location: "Test City", Phone: "111", Website: "https://a.example"},
		{Name: "Co B", Location: "Test City", Phone: "222", Website: "https://b.example"},
	}

	saved, err := repo.SaveSearchWithCompanies(ctx, search, companies)
	if err != nil {
		t.Fatalf("SaveSearchWithCompanies: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM searches WHERE id = $1::uuid`, search.ID)
	})

	if search.ID == "" {
		t.Fatal("expected search.ID to be populated from RETURNING")
	}
	if search.CreatedAt.IsZero() {
		t.Fatal("expected search.CreatedAt to be populated")
	}
	if len(saved) != 2 {
		t.Fatalf("expected 2 saved companies, got %d", len(saved))
	}
	for _, c := range saved {
		if c.ID == "" {
			t.Error("expected company.ID populated")
		}
		if c.SearchID != search.ID {
			t.Errorf("expected company.SearchID %q, got %q", search.ID, c.SearchID)
		}
		if c.CreatedAt.IsZero() {
			t.Error("expected company.CreatedAt populated")
		}
	}

	// Confirm the rows really landed and are linked via search_id.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM companies WHERE search_id = $1::uuid`, search.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count companies: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 company rows in db, got %d", count)
	}
}

func TestSaveSearchWithCompanies_EmptyList(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewSearchRepository(pool)
	ctx := context.Background()

	search := &domain.Search{Niche: "empty-niche", Location: "Nowhere", Quantity: 0}
	saved, err := repo.SaveSearchWithCompanies(ctx, search, nil)
	if err != nil {
		t.Fatalf("SaveSearchWithCompanies (empty): %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM searches WHERE id = $1::uuid`, search.ID)
	})

	if search.ID == "" {
		t.Fatal("expected search.ID populated even with no companies")
	}
	if len(saved) != 0 {
		t.Errorf("expected 0 saved companies, got %d", len(saved))
	}
}
