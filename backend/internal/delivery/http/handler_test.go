package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
	"github.com/zennitex/clicars-search/internal/usecase"
)

// uuidRe validates the canonical 8-4-4-4-12 UUID layout.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// fakeSearcher is a stub so the HTTP layer can be tested without a real usecase.
type fakeSearcher struct {
	result *usecase.SearchResult
	err    error
}

func (f *fakeSearcher) Execute(_ context.Context, _ usecase.SearchInput) (*usecase.SearchResult, error) {
	return f.result, f.err
}

// fakeHistoryReader stubs the HistoryReader interface.
type fakeHistoryReader struct {
	searches  []domain.Search
	search    *domain.Search
	companies []domain.Company
	err       error
}

func (f *fakeHistoryReader) GetSearches(_ context.Context, _ int) ([]domain.Search, error) {
	if f.searches == nil {
		return []domain.Search{}, f.err
	}
	return f.searches, f.err
}

func (f *fakeHistoryReader) GetSearchByID(_ context.Context, _ string) (*domain.Search, []domain.Company, error) {
	return f.search, f.companies, f.err
}

var emptyHistory = &fakeHistoryReader{}

func newTestServer(s Searcher) *httptest.Server {
	mux := http.NewServeMux()
	NewSearchHandler(s, emptyHistory).RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

// TestHandleSearch_Accepted checks the happy-path POST contract:
// 202 Accepted + JSON body with a valid UUID search_id and status PENDING.
func TestHandleSearch_Accepted(t *testing.T) {
	const searchID = "b3f1c2a4-5d6e-7f80-91a2-b3c4d5e6f708"
	stub := &fakeSearcher{result: &usecase.SearchResult{
		SearchID: searchID,
		Status:   domain.SearchStatusPending,
	}}

	srv := newTestServer(stub)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/searches", "application/json",
		strings.NewReader(`{"niche":"padarias","location":"São Paulo","quantity":2}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", resp.StatusCode)
	}

	var decoded searchStartedResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !uuidRe.MatchString(decoded.SearchID) {
		t.Errorf("expected a UUID search_id, got %q", decoded.SearchID)
	}
	if decoded.Status != domain.SearchStatusPending {
		t.Errorf("expected status PENDING, got %q", decoded.Status)
	}
}

func TestHandleSearch_BadJSON(t *testing.T) {
	srv := newTestServer(&fakeSearcher{})
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/searches", "application/json",
		strings.NewReader(`{not valid json`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}
}

// TestHandleSearch_ErrorMapping checks the sentinel-error → HTTP status translation.
func TestHandleSearch_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"invalid input -> 400", fmt.Errorf("%w: bad", usecase.ErrInvalidInput), http.StatusBadRequest},
		{"upstream -> 502", fmt.Errorf("%w: provider", usecase.ErrUpstream), http.StatusBadGateway},
		{"persistence -> 500", fmt.Errorf("%w: db", usecase.ErrPersistence), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(&fakeSearcher{err: tc.err})
			defer srv.Close()

			resp, err := http.Post(srv.URL+"/api/v1/searches", "application/json",
				strings.NewReader(`{"niche":"x","location":"y","quantity":1}`))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("expected status %d, got %d", tc.want, resp.StatusCode)
			}
		})
	}
}

func TestHandleSearch_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(&fakeSearcher{})
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/searches", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.StatusCode)
	}
}

func TestHandleListSearches_OK(t *testing.T) {
	history := &fakeHistoryReader{
		searches: []domain.Search{
			{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Niche: "padarias", Location: "SP", Quantity: 5, Status: domain.SearchStatusCompleted},
		},
	}

	mux := http.NewServeMux()
	NewSearchHandler(&fakeSearcher{}, history).RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/searches")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var decoded []domain.Search
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded) != 1 {
		t.Errorf("expected 1 search, got %d", len(decoded))
	}
}

func TestHandleGetSearch_NotFound(t *testing.T) {
	history := &fakeHistoryReader{err: domain.ErrNotFound}

	mux := http.NewServeMux()
	NewSearchHandler(&fakeSearcher{}, history).RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/searches/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

// TestHandleGetSearch_WithProgress checks that a RUNNING search includes
// estimated_seconds_remaining in the response.
func TestHandleGetSearch_WithProgress(t *testing.T) {
	now := time.Now().Add(-30 * time.Second)
	history := &fakeHistoryReader{
		search: &domain.Search{
			ID:        "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			Niche:     "padarias",
			Location:  "SP",
			Quantity:  100,
			Status:    domain.SearchStatusRunning,
			Progress:  30,
			StartedAt: &now,
		},
		companies: []domain.Company{},
	}

	mux := http.NewServeMux()
	NewSearchHandler(&fakeSearcher{}, history).RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/searches/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var decoded searchDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Status != domain.SearchStatusRunning {
		t.Errorf("expected status RUNNING, got %q", decoded.Status)
	}
	if decoded.EstimatedSecondsRemaining == nil {
		t.Error("expected estimated_seconds_remaining for a running search with progress > 0")
	}
}
