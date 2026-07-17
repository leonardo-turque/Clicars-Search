package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/zennitex/clicars-search/internal/domain"
)

// ── Test doubles ──────────────────────────────────────────────────────────────

// fakeFinder is a stub CompanyFinder. It records arguments and returns canned data.
type fakeFinder struct {
	companies []domain.Company
	err       error

	called      int
	gotNiche    string
	gotLocation string
	gotQuantity int
}

func (f *fakeFinder) SearchCompanies(_ context.Context, niche, location string, quantity int, _ func(int)) ([]domain.Company, error) {
	f.called++
	f.gotNiche, f.gotLocation, f.gotQuantity = niche, location, quantity
	return f.companies, f.err
}

// fakeStore is a stub AsyncSearchStore. It simulates the repository by writing
// back a synthetic ID on CreateSearch and recording lifecycle calls.
type fakeStore struct {
	createErr    error
	saveErr      error
	completedErr error

	createdSearch  *domain.Search
	savedCompanies []domain.Company
	completedID    string
	completedCount int
	failedID       string
	failedMsg      string
}

func (s *fakeStore) CreateSearch(_ context.Context, search *domain.Search) error {
	if s.createErr != nil {
		return s.createErr
	}
	search.ID = "11111111-1111-1111-1111-111111111111"
	s.createdSearch = search
	return nil
}

func (s *fakeStore) SetSearchRunning(_ context.Context, _ string) error { return nil }

func (s *fakeStore) UpdateSearchProgress(_ context.Context, _ string, _ int) error { return nil }

func (s *fakeStore) SaveSearchCompanies(_ context.Context, searchID string, companies []domain.Company) ([]domain.Company, error) {
	if s.saveErr != nil {
		return nil, s.saveErr
	}
	saved := make([]domain.Company, len(companies))
	copy(saved, companies)
	for i := range saved {
		saved[i].SearchID = searchID
	}
	s.savedCompanies = companies
	return saved, nil
}

func (s *fakeStore) SetSearchCompleted(_ context.Context, id string, progress int) error {
	s.completedID = id
	s.completedCount = progress
	return s.completedErr
}

func (s *fakeStore) SetSearchFailed(_ context.Context, id, msg string) error {
	s.failedID = id
	s.failedMsg = msg
	return nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// newSync wraps newPerformSearchSync so tests run the background goroutine
// synchronously, making assertions deterministic without time.Sleep.
func newSync(finder CompanyFinder, store AsyncSearchStore) *PerformSearch {
	return newPerformSearchSync(finder, store)
}

func TestExecute_HappyPath(t *testing.T) {
	finder := &fakeFinder{companies: []domain.Company{
		{Name: "Alpha Auto", Website: "https://a.example"},
		{Name: "Beta Cars", Website: "https://b.example"},
	}}
	store := &fakeStore{}
	uc := newSync(finder, store)

	res, err := uc.Execute(context.Background(), SearchInput{
		Niche: "concessionárias", Location: "São Paulo", Quantity: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected a result, got nil")
	}
	if res.SearchID == "" {
		t.Error("expected search ID to be populated")
	}
	if res.Status != domain.SearchStatusPending {
		t.Errorf("expected status PENDING, got %q", res.Status)
	}
	if store.completedID == "" {
		t.Error("expected SetSearchCompleted to be called after background work")
	}
	if store.completedCount != 2 {
		t.Errorf("expected completed with 2 companies, got %d", store.completedCount)
	}
	if len(store.savedCompanies) != 2 {
		t.Errorf("expected 2 saved companies, got %d", len(store.savedCompanies))
	}
	if finder.called != 1 {
		t.Errorf("expected finder called once, got %d", finder.called)
	}
}

func TestExecute_TrimsInput(t *testing.T) {
	finder := &fakeFinder{}
	store := &fakeStore{}
	uc := newSync(finder, store)

	if _, err := uc.Execute(context.Background(), SearchInput{
		Niche: "  padarias  ", Location: "  São Paulo  ", Quantity: 5,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if finder.gotNiche != "padarias" {
		t.Errorf("expected trimmed niche %q, got %q", "padarias", finder.gotNiche)
	}
	if finder.gotLocation != "São Paulo" {
		t.Errorf("expected trimmed location %q, got %q", "São Paulo", finder.gotLocation)
	}
}

func TestExecute_EmptyResults(t *testing.T) {
	finder := &fakeFinder{companies: []domain.Company{}}
	store := &fakeStore{}
	uc := newSync(finder, store)

	res, err := uc.Execute(context.Background(), SearchInput{
		Niche: "inexistente", Location: "Nowhere", Quantity: 3,
	})
	if err != nil {
		t.Fatalf("expected no error on empty results, got: %v", err)
	}
	if res.SearchID == "" {
		t.Error("expected search ID even for empty results")
	}
	// Empty search is still persisted and completed.
	if store.completedID == "" {
		t.Error("expected SetSearchCompleted to be called even for empty results")
	}
}

// TestExecute_FinderError verifies a scraping failure causes SetSearchFailed
// to be called (not surfaced as a synchronous Execute error).
func TestExecute_FinderError(t *testing.T) {
	finder := &fakeFinder{err: errors.New("maps unavailable")}
	store := &fakeStore{}
	uc := newSync(finder, store)

	res, err := uc.Execute(context.Background(), SearchInput{Niche: "x", Location: "y", Quantity: 1})
	if err != nil {
		t.Fatalf("Execute should succeed synchronously even when finder fails later; got: %v", err)
	}
	if res.SearchID == "" {
		t.Error("expected search ID")
	}
	if store.failedID == "" {
		t.Error("expected SetSearchFailed to be called after goroutine error")
	}
	if store.completedID != "" {
		t.Error("expected SetSearchCompleted NOT to be called when finder fails")
	}
}

// TestExecute_CreateSearchError verifies a DB creation failure is returned
// synchronously as ErrPersistence.
func TestExecute_CreateSearchError(t *testing.T) {
	finder := &fakeFinder{}
	store := &fakeStore{createErr: errors.New("db down")}
	uc := newSync(finder, store)

	_, err := uc.Execute(context.Background(), SearchInput{Niche: "x", Location: "y", Quantity: 1})
	if !errors.Is(err, ErrPersistence) {
		t.Fatalf("expected ErrPersistence, got %v", err)
	}
}

// TestExecute_SaveCompaniesError verifies a companies save failure causes
// SetSearchFailed to be called (background error, not synchronous).
func TestExecute_SaveCompaniesError(t *testing.T) {
	finder := &fakeFinder{companies: []domain.Company{{Name: "A"}}}
	store := &fakeStore{saveErr: errors.New("db constraint")}
	uc := newSync(finder, store)

	res, err := uc.Execute(context.Background(), SearchInput{Niche: "x", Location: "y", Quantity: 1})
	if err != nil {
		t.Fatalf("Execute should succeed synchronously; got: %v", err)
	}
	if res.SearchID == "" {
		t.Error("expected search ID")
	}
	if store.failedID == "" {
		t.Error("expected SetSearchFailed to be called when SaveSearchCompanies fails")
	}
}

func TestExecute_ValidationErrors(t *testing.T) {
	cases := map[string]SearchInput{
		"empty niche":       {Niche: "", Location: "SP", Quantity: 1},
		"whitespace niche":  {Niche: "   ", Location: "SP", Quantity: 1},
		"empty location":    {Niche: "padarias", Location: "", Quantity: 1},
		"zero quantity":     {Niche: "padarias", Location: "SP", Quantity: 0},
		"negative quantity": {Niche: "padarias", Location: "SP", Quantity: -3},
		"quantity over max": {Niche: "padarias", Location: "SP", Quantity: maxQuantity + 1},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			finder := &fakeFinder{}
			store := &fakeStore{}
			uc := newSync(finder, store)

			_, err := uc.Execute(context.Background(), in)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
			if finder.called != 0 || store.createdSearch != nil {
				t.Error("expected no downstream calls on invalid input")
			}
		})
	}
}

func TestExecute_MaxQuantityBoundary(t *testing.T) {
	finder := &fakeFinder{}
	store := &fakeStore{}
	uc := newSync(finder, store)

	if _, err := uc.Execute(context.Background(), SearchInput{
		Niche: "padarias", Location: "SP", Quantity: maxQuantity,
	}); err != nil {
		t.Fatalf("expected quantity==%d to be valid, got %v", maxQuantity, err)
	}
	if finder.gotQuantity != maxQuantity {
		t.Errorf("expected finder to receive quantity %d, got %d", maxQuantity, finder.gotQuantity)
	}
}
