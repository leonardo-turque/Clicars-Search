package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
)

// maxQuantity caps how many companies a single search may request.
const maxQuantity = 5000

// Sentinel errors let the delivery layer translate failures into HTTP statuses.
var (
	ErrInvalidInput = errors.New("invalid input")
	ErrUpstream     = errors.New("company provider failed")
	ErrPersistence  = errors.New("failed to persist search")
)

// CompanyFinder is the port for any provider able to discover companies.
// onProgress is called with a running total of enriched companies; may be nil.
type CompanyFinder interface {
	SearchCompanies(ctx context.Context, niche, location string, quantity int, onProgress func(int)) ([]domain.Company, error)
}

// AsyncSearchStore is the port for managing the async search lifecycle.
type AsyncSearchStore interface {
	CreateSearch(ctx context.Context, search *domain.Search) error
	SetSearchRunning(ctx context.Context, id string) error
	UpdateSearchProgress(ctx context.Context, id string, progress int) error
	SaveSearchCompanies(ctx context.Context, searchID string, companies []domain.Company) ([]domain.Company, error)
	SetSearchCompleted(ctx context.Context, id string, progress int) error
	SetSearchFailed(ctx context.Context, id string, msg string) error
}

// SearchInput is the validated request handed to the usecase.
type SearchInput struct {
	Niche    string
	Location string
	Quantity int
}

// SearchResult is returned immediately after creating the async job.
type SearchResult struct {
	SearchID string
	Status   string
}

// PerformSearch orchestrates the async search flow.
type PerformSearch struct {
	finder CompanyFinder
	store  AsyncSearchStore
	// bgRun launches a function in the background. Defaults to `go fn()`.
	// Tests override it with a synchronous runner to avoid goroutine races.
	bgRun func(func())
}

func NewPerformSearch(finder CompanyFinder, store AsyncSearchStore) *PerformSearch {
	return &PerformSearch{
		finder: finder,
		store:  store,
		bgRun:  func(fn func()) { go fn() },
	}
}

// newPerformSearchSync is used in tests to run the background work synchronously.
func newPerformSearchSync(finder CompanyFinder, store AsyncSearchStore) *PerformSearch {
	return &PerformSearch{finder: finder, store: store, bgRun: func(fn func()) { fn() }}
}

// Execute validates the input, creates the search record in the DB with status
// PENDING, then immediately returns the search ID while the actual scraping
// runs in the background. The caller polls GET /searches/{id} for progress.
func (uc *PerformSearch) Execute(ctx context.Context, in SearchInput) (*SearchResult, error) {
	in.Niche = strings.TrimSpace(in.Niche)
	in.Location = strings.TrimSpace(in.Location)
	if err := in.validate(); err != nil {
		return nil, err
	}

	search := &domain.Search{
		Niche:    in.Niche,
		Location: in.Location,
		Quantity: in.Quantity,
		Status:   domain.SearchStatusPending,
	}

	if err := uc.store.CreateSearch(ctx, search); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPersistence, err)
	}

	searchID := search.ID
	niche, location, quantity := in.Niche, in.Location, in.Quantity

	uc.bgRun(func() {
		bgCtx := context.Background()

		if err := uc.store.SetSearchRunning(bgCtx, searchID); err != nil {
			log.Printf("search %s: mark running: %v", searchID, err)
		}

		// Track enrichment progress atomically; a ticker flushes it to the DB
		// every 3 s so the frontend can poll without hammering the database.
		var enriched atomic.Int64

		ticker := time.NewTicker(3 * time.Second)
		tickerDone := make(chan struct{})
		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					p := int(enriched.Load())
					if err := uc.store.UpdateSearchProgress(bgCtx, searchID, p); err != nil {
						log.Printf("search %s: update progress: %v", searchID, err)
					}
				case <-tickerDone:
					return
				}
			}
		}()

		companies, err := uc.finder.SearchCompanies(bgCtx, niche, location, quantity, func(found int) {
			enriched.Store(int64(found))
		})
		close(tickerDone)

		if err != nil {
			log.Printf("search %s: scraping failed: %v", searchID, err)
			if dbErr := uc.store.SetSearchFailed(bgCtx, searchID, err.Error()); dbErr != nil {
				log.Printf("search %s: mark failed: %v", searchID, dbErr)
			}
			return
		}

		if _, err := uc.store.SaveSearchCompanies(bgCtx, searchID, companies); err != nil {
			log.Printf("search %s: save companies failed: %v", searchID, err)
			if dbErr := uc.store.SetSearchFailed(bgCtx, searchID, err.Error()); dbErr != nil {
				log.Printf("search %s: mark failed: %v", searchID, dbErr)
			}
			return
		}

		if err := uc.store.SetSearchCompleted(bgCtx, searchID, len(companies)); err != nil {
			log.Printf("search %s: mark completed: %v", searchID, err)
		}
	})

	return &SearchResult{SearchID: searchID, Status: domain.SearchStatusPending}, nil
}

func (in SearchInput) validate() error {
	switch {
	case in.Niche == "":
		return fmt.Errorf("%w: niche is required", ErrInvalidInput)
	case in.Location == "":
		return fmt.Errorf("%w: location is required", ErrInvalidInput)
	case in.Quantity <= 0:
		return fmt.Errorf("%w: quantity must be greater than zero", ErrInvalidInput)
	case in.Quantity > maxQuantity:
		return fmt.Errorf("%w: quantity must not exceed %d", ErrInvalidInput, maxQuantity)
	}
	return nil
}
