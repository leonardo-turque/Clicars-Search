//go:build integration

// Integration tests for the WhatsApp session persistence layer. They require a
// running PostgreSQL with the schema from db/init.sql applied.
//
// Run with:
//
//	docker compose up -d db
//	go test -tags=integration ./internal/repository/...
//
// Override the connection with TEST_DATABASE_URL if needed (see testPool in
// search_integration_test.go, which this file reuses).
package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/zennitex/clicars-search/internal/domain"
)

func TestWhatsAppRepository_CRUD_Integration(t *testing.T) {
	pool := testPool(t)
	// Register Close first so, by Cleanup's LIFO order, it runs AFTER the row
	// deletions below (a deferred Close would run before them, on a live pool).
	t.Cleanup(func() { pool.Close() })

	repo := NewWhatsAppRepository(pool)
	ctx := context.Background()

	const id = "aa000000-0000-4000-8000-000000000001"
	const jid = "5511988887777:3@s.whatsapp.net"
	const phone = "5511988887777"

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM whatsapp_sessions WHERE id = $1::uuid`, id)
	})

	// Insert
	s, err := repo.Insert(ctx, id, jid, phone, domain.WhatsAppConnected)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if s.ID != id || s.JID != jid || s.PhoneNumber != phone || s.Status != domain.WhatsAppConnected {
		t.Fatalf("unexpected inserted row: %+v", s)
	}
	if s.CreatedAt.IsZero() {
		t.Error("expected created_at populated")
	}

	// GetByID
	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.JID != jid {
		t.Errorf("expected jid %q, got %q", jid, got.JID)
	}

	// List contains it
	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !containsID(all, id) {
		t.Errorf("expected List to contain %s", id)
	}

	// Update (new jid + status)
	const jid2 = "5511988887777:4@s.whatsapp.net"
	if err := repo.Update(ctx, id, jid2, phone, domain.WhatsAppDisconnected); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = repo.GetByID(ctx, id)
	if got.JID != jid2 || got.Status != domain.WhatsAppDisconnected {
		t.Errorf("update not applied: %+v", got)
	}

	// UpdateStatus back to connected
	if err := repo.UpdateStatus(ctx, id, domain.WhatsAppConnected); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = repo.GetByID(ctx, id)
	if got.Status != domain.WhatsAppConnected {
		t.Errorf("expected CONNECTED, got %q", got.Status)
	}

	// Delete
	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestWhatsAppRepository_DeleteOldDisconnected_Integration(t *testing.T) {
	pool := testPool(t)
	// Register Close first so, by Cleanup's LIFO order, it runs AFTER the row
	// deletions below (a deferred Close would run before them, on a live pool).
	t.Cleanup(func() { pool.Close() })

	repo := NewWhatsAppRepository(pool)
	ctx := context.Background()

	const connID = "aa000000-0000-4000-8000-000000000010"
	const discID = "aa000000-0000-4000-8000-000000000011"
	t.Cleanup(func() {
		for _, id := range []string{connID, discID} {
			_, _ = pool.Exec(context.Background(), `DELETE FROM whatsapp_sessions WHERE id = $1::uuid`, id)
		}
	})

	if _, err := repo.Insert(ctx, connID, "j1:1@s.whatsapp.net", "111", domain.WhatsAppConnected); err != nil {
		t.Fatalf("insert connected: %v", err)
	}
	if _, err := repo.Insert(ctx, discID, "j2:1@s.whatsapp.net", "222", domain.WhatsAppDisconnected); err != nil {
		t.Fatalf("insert disconnected: %v", err)
	}

	// days=0 ⇒ "older than now": removes only the DISCONNECTED row.
	n, err := repo.DeleteOldDisconnected(ctx, 0)
	if err != nil {
		t.Fatalf("DeleteOldDisconnected: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 deleted, got %d", n)
	}
	if _, err := repo.GetByID(ctx, discID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected disconnected row removed, got %v", err)
	}
	if _, err := repo.GetByID(ctx, connID); err != nil {
		t.Errorf("expected connected row to survive, got %v", err)
	}
}

func containsID(sessions []domain.WhatsAppSession, id string) bool {
	for _, s := range sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}
