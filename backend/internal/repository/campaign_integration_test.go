//go:build integration

// Integration tests for the campaign persistence layer. They require a running
// PostgreSQL with the schema from db/init.sql applied.
//
// Run with:
//
//	docker compose up -d db
//	go test -tags=integration ./internal/repository/...
//
// Override the connection with TEST_DATABASE_URL if needed (testPool lives in
// search_integration_test.go, which this file reuses).
package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/zennitex/clicars-search/internal/domain"
)

func TestCampaignRepository_Integration(t *testing.T) {
	pool := testPool(t)
	// Register Close first so, by Cleanup's LIFO order, it runs AFTER the row
	// deletions below (a deferred Close would run before them, on a live pool).
	t.Cleanup(func() { pool.Close() })

	searchRepo := NewSearchRepository(pool)
	repo := NewCampaignRepository(pool)
	ctx := context.Background()

	// Seed a search with two companies — one with a phone, one without — so we can
	// assert GetPhonesBySearchID filters out the blank.
	search := &domain.Search{Niche: "campaign-niche", Location: "Test City", Quantity: 2}
	if _, err := searchRepo.SaveSearchWithCompanies(ctx, search, []domain.Company{
		{Name: "Co A", Phone: "5511999990001"},
		{Name: "Co B", Phone: ""},
	}); err != nil {
		t.Fatalf("seed search: %v", err)
	}
	// Deleting the search cascades to its companies AND campaigns.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM searches WHERE id = $1::uuid`, search.ID)
	})

	// GetPhonesBySearchID returns only the non-empty phone.
	phones, err := repo.GetPhonesBySearchID(ctx, search.ID)
	if err != nil {
		t.Fatalf("GetPhonesBySearchID: %v", err)
	}
	if len(phones) != 1 || phones[0] != "5511999990001" {
		t.Fatalf("expected exactly the non-empty phone, got %v", phones)
	}

	// Create
	c := &domain.Campaign{
		SearchID:          search.ID,
		WhatsAppSessionID: "bb000000-0000-4000-8000-000000000001",
		MessageBody:       "Olá, tudo bem?",
		Status:            domain.CampaignPending,
		Total:            1,
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == "" || c.CreatedAt.IsZero() {
		t.Fatalf("expected id/created_at populated, got %+v", c)
	}

	// GetByID
	got, err := repo.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.SearchID != search.ID || got.MessageBody != "Olá, tudo bem?" || got.Total != 1 {
		t.Errorf("unexpected campaign read back: %+v", got)
	}

	// SetProgress + UpdateStatus
	if err := repo.SetProgress(ctx, c.ID, 1, 0); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	if err := repo.UpdateStatus(ctx, c.ID, domain.CampaignCompleted); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = repo.GetByID(ctx, c.ID)
	if got.Sent != 1 || got.Status != domain.CampaignCompleted {
		t.Errorf("progress/status not applied: %+v", got)
	}

	// DeleteOldCampaigns(0) ⇒ "older than now" removes the row.
	n, err := repo.DeleteOldCampaigns(ctx, 0)
	if err != nil {
		t.Fatalf("DeleteOldCampaigns: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 campaign deleted, got %d", n)
	}
	if _, err := repo.GetByID(ctx, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

// not-found paths on a fresh repo.
func TestCampaignRepository_NotFound_Integration(t *testing.T) {
	pool := testPool(t)
	t.Cleanup(func() { pool.Close() })
	repo := NewCampaignRepository(pool)
	ctx := context.Background()

	const missing = "bb000000-0000-4000-8000-0000000000ff"
	if _, err := repo.GetByID(ctx, missing); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID: expected ErrNotFound, got %v", err)
	}
	if err := repo.UpdateStatus(ctx, missing, domain.CampaignRunning); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateStatus: expected ErrNotFound, got %v", err)
	}
	if err := repo.SetProgress(ctx, missing, 1, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetProgress: expected ErrNotFound, got %v", err)
	}
}
