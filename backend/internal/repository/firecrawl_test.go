package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestRepo(baseURL string) *FirecrawlRepository {
	return &FirecrawlRepository{
		apiKey:         "test-key",
		baseURL:        baseURL,
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		maxRetries:     1,
		maxConcurrency: 4,
	}
}

func respondJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func searchPayload(results ...map[string]any) map[string]any {
	return map[string]any{
		"success": true,
		"data":    map[string]any{"web": results},
	}
}

// TestSearchCompanies_HappyPath verifies the full search -> concurrent enrich
// flow: results are mapped to companies and the scrape extraction fills phone
// and location.
func TestSearchCompanies_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("expected bearer auth header, got %q", got)
		}
		respondJSON(w, searchPayload(
			map[string]any{"url": "https://a.example", "title": "Alpha Auto", "description": "d"},
			map[string]any{"url": "https://b.example", "title": "Beta Cars", "description": "d"},
		))
	})
	mux.HandleFunc("/v2/scrape", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, map[string]any{
			"success": true,
			"data": map[string]any{
				"json":     map[string]any{"phone": "+55 11 4002-8922", "location": "São Paulo"},
				"markdown": "ignored",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	companies, err := newTestRepo(srv.URL).SearchCompanies(context.Background(), "concessionárias", "São Paulo", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(companies) != 2 {
		t.Fatalf("expected 2 companies, got %d", len(companies))
	}
	for _, c := range companies {
		if c.Name == "" || c.Website == "" {
			t.Errorf("expected name/website from search result, got %+v", c)
		}
		if c.Phone != "+55 11 4002-8922" {
			t.Errorf("expected phone from enrichment, got %q", c.Phone)
		}
		if c.Location != "São Paulo" {
			t.Errorf("expected location São Paulo, got %q", c.Location)
		}
	}
}

// TestSearchCompanies_GracefulDegradation verifies that when enrichment fails
// for every result, the search still succeeds with the base data from the
// search results (no error bubbles up).
func TestSearchCompanies_GracefulDegradation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/search", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, searchPayload(
			map[string]any{"url": "https://a.example", "title": "Alpha Auto", "description": "d"},
		))
	})
	mux.HandleFunc("/v2/scrape", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	companies, err := newTestRepo(srv.URL).SearchCompanies(context.Background(), "x", "Rio", 1)
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if len(companies) != 1 {
		t.Fatalf("expected 1 company, got %d", len(companies))
	}
	if companies[0].Name != "Alpha Auto" || companies[0].Location != "Rio" {
		t.Errorf("expected base data preserved, got %+v", companies[0])
	}
	if companies[0].Phone != "" {
		t.Errorf("expected empty phone when enrichment fails, got %q", companies[0].Phone)
	}
}

// TestSearch_RetriesThenSucceeds verifies the retry/backoff logic: a transient
// 503 on the first attempt is retried and the second attempt succeeds.
func TestSearch_RetriesThenSucceeds(t *testing.T) {
	var searchCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/search", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&searchCalls, 1) == 1 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		respondJSON(w, searchPayload(
			map[string]any{"url": "https://a.example", "title": "Alpha", "description": "d"},
		))
	})
	mux.HandleFunc("/v2/scrape", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, map[string]any{"success": true, "data": map[string]any{"json": map[string]any{}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	companies, err := newTestRepo(srv.URL).SearchCompanies(context.Background(), "x", "y", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(companies) != 1 {
		t.Fatalf("expected 1 company, got %d", len(companies))
	}
	if got := atomic.LoadInt32(&searchCalls); got < 2 {
		t.Errorf("expected the search to be retried (>=2 calls), got %d", got)
	}
}

// TestSearchCompanies_NoAPIKey verifies the repository fails fast (and clearly)
// when no API key is configured, rather than making a doomed HTTP call.
func TestSearchCompanies_NoAPIKey(t *testing.T) {
	repo := newTestRepo("http://unused.invalid")
	repo.apiKey = ""
	if _, err := repo.SearchCompanies(context.Background(), "x", "y", 1); err == nil {
		t.Fatal("expected an error when the API key is missing")
	}
}

// TestFirstPhone exercises the regex phone fallback used when structured
// extraction does not return a phone number.
func TestFirstPhone(t *testing.T) {
	cases := map[string]string{
		"Ligue para +55 (11) 4002-8922 agora": "+55 (11) 4002-8922",
		"no phone here, just 42 apples":        "",
		"":                                     "",
	}
	for in, want := range cases {
		if got := firstPhone(in); got != want {
			t.Errorf("firstPhone(%q) = %q, want %q", in, got, want)
		}
	}
}
