package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zennitex/clicars-search/internal/domain"
)

// Firecrawl integration tuning. These are intentionally conservative so that a
// single misbehaving upstream call can never hold an HTTP request hostage.
const (
	defaultBaseURL     = "https://api.firecrawl.dev"
	httpClientTimeout  = 45 * time.Second
	defaultMaxRetries  = 2
	defaultConcurrency = 5
	maxBackoff         = 4 * time.Second
	maxResponseBytes   = 5 << 20 // 5 MiB safety cap on response bodies

	extractionPrompt = "Extract the company or business name, the primary contact " +
		"phone number, the official website URL and the city or address from this page."
)

// companySchema is the JSON Schema sent to Firecrawl's structured-extraction
// engine so it returns predictable fields for each scraped page.
var companySchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name":     {"type": "string", "description": "Company or business name"},
    "phone":    {"type": "string", "description": "Primary contact phone number"},
    "website":  {"type": "string", "description": "Official website URL"},
    "location": {"type": "string", "description": "City or full address"}
  }
}`)

// FirecrawlRepository talks to the Firecrawl API to discover companies for a
// given niche/location. It implements usecase.CompanyFinder.
type FirecrawlRepository struct {
	apiKey         string
	baseURL        string
	httpClient     *http.Client
	maxRetries     int
	maxConcurrency int
}

// NewFirecrawlRepository builds the repository. The base URL can be overridden
// with FIRECRAWL_API_URL (handy for tests / self-hosted Firecrawl instances).
func NewFirecrawlRepository(apiKey string) *FirecrawlRepository {
	baseURL := strings.TrimRight(os.Getenv("FIRECRAWL_API_URL"), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &FirecrawlRepository{
		apiKey:         apiKey,
		baseURL:        baseURL,
		httpClient:     &http.Client{Timeout: httpClientTimeout},
		maxRetries:     defaultMaxRetries,
		maxConcurrency: defaultConcurrency,
	}
}

// SearchCompanies queries Firecrawl for `quantity` companies matching the niche
// and location, then concurrently enriches each result with contact details.
//
// Enrichment is best-effort: a failure to scrape an individual page degrades
// gracefully to the data already known from the search result, so a few flaky
// pages never sink the whole batch.
func (r *FirecrawlRepository) SearchCompanies(ctx context.Context, niche, location string, quantity int) ([]domain.Company, error) {
	if r.apiKey == "" {
		return nil, errors.New("firecrawl api key not configured (set FIRECRAWL_API_KEY)")
	}
	if quantity <= 0 {
		quantity = 1
	}

	results, err := r.search(ctx, niche, location, quantity)
	if err != nil {
		return nil, err
	}

	companies := make([]domain.Company, len(results))
	for i, res := range results {
		companies[i] = domain.Company{
			Name:     strings.TrimSpace(res.Title),
			Website:  res.URL,
			Location: location,
		}
	}

	r.enrichAll(ctx, companies)
	return companies, nil
}

// search performs the Firecrawl web search and returns the raw web results.
func (r *FirecrawlRepository) search(ctx context.Context, niche, location string, limit int) ([]webResult, error) {
	query := strings.TrimSpace(niche)
	if location != "" {
		query = strings.TrimSpace(query + " " + location)
	}

	reqBody := searchAPIRequest{
		Query:    query,
		Limit:    limit,
		Location: location,
		Sources:  []sourceSpec{{Type: "web"}},
	}

	var out searchAPIResponse
	if err := r.doJSON(ctx, "/v2/search", reqBody, &out); err != nil {
		return nil, fmt.Errorf("firecrawl search: %w", err)
	}
	if !out.Success {
		return nil, fmt.Errorf("firecrawl search unsuccessful: %s", strings.TrimSpace(out.Error))
	}

	web := out.Data.Web
	if len(web) > limit {
		web = web[:limit]
	}
	return web, nil
}

// enrichAll fans out one scrape request per company, bounded by a semaphore so
// we never open more than maxConcurrency connections to Firecrawl at once.
// Each goroutine writes to a distinct slice index, so no locking is required.
func (r *FirecrawlRepository) enrichAll(ctx context.Context, companies []domain.Company) {
	sem := make(chan struct{}, r.maxConcurrency)
	var wg sync.WaitGroup

	for i := range companies {
		if companies[i].Website == "" {
			continue
		}

		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			// Acquire a slot, but bail out promptly if the request is cancelled.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			extracted, markdown, err := r.scrapeExtract(ctx, companies[i].Website)
			if err != nil {
				log.Printf("firecrawl: enriching %q failed (keeping base data): %v", companies[i].Website, err)
				return
			}

			c := merge(companies[i], extracted)
			if c.Phone == "" {
				c.Phone = firstPhone(markdown)
			}
			companies[i] = c
		}(i)
	}

	wg.Wait()
}

// scrapeExtract scrapes a single URL asking Firecrawl for both structured JSON
// (via the schema) and markdown (used as a regex fallback for the phone number).
func (r *FirecrawlRepository) scrapeExtract(ctx context.Context, url string) (extractedCompany, string, error) {
	reqBody := scrapeAPIRequest{
		URL: url,
		Formats: []formatSpec{
			{Type: "json", Schema: companySchema, Prompt: extractionPrompt},
			{Type: "markdown"},
		},
	}

	var out scrapeAPIResponse
	if err := r.doJSON(ctx, "/v2/scrape", reqBody, &out); err != nil {
		return extractedCompany{}, "", err
	}
	if !out.Success {
		return extractedCompany{}, "", fmt.Errorf("firecrawl scrape unsuccessful: %s", strings.TrimSpace(out.Error))
	}
	return out.Data.JSON, out.Data.Markdown, nil
}

// doJSON performs a POST request with retry + exponential backoff. Network
// errors, HTTP 429 and 5xx responses are retried; 4xx responses fail fast since
// retrying a malformed/unauthorized request is pointless. Context cancellation
// is honoured at every step.
func (r *FirecrawlRepository) doJSON(ctx context.Context, path string, reqBody, out any) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	backoff := 500 * time.Millisecond
	var lastErr error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			wait := backoff + time.Duration(rand.Int63n(int64(250*time.Millisecond)))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = fmt.Errorf("request to %s failed: %w", path, err)
			continue // transient network error -> retry
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("decode response from %s: %w", path, err)
			}
			return nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("firecrawl %s returned %d: %s", path, resp.StatusCode, snippet(body))
			continue // transient server-side error -> retry
		default:
			return fmt.Errorf("firecrawl %s returned %d: %s", path, resp.StatusCode, snippet(body))
		}
	}

	return fmt.Errorf("exhausted %d retries: %w", r.maxRetries, lastErr)
}

// merge fills the gaps in base with extracted data without clobbering values we
// already trust from the search result.
func merge(base domain.Company, ex extractedCompany) domain.Company {
	if base.Name == "" {
		base.Name = strings.TrimSpace(ex.Name)
	}
	if base.Phone == "" {
		base.Phone = strings.TrimSpace(ex.Phone)
	}
	if base.Website == "" {
		base.Website = strings.TrimSpace(ex.Website)
	}
	if base.Location == "" {
		base.Location = strings.TrimSpace(ex.Location)
	}
	return base
}

// phoneRe matches loosely-formatted phone numbers; firstPhone then validates the
// digit count so we don't mistake arbitrary numbers for phones.
var phoneRe = regexp.MustCompile(`\+?\d[\d\s().-]{7,}\d`)

func firstPhone(text string) string {
	for _, m := range phoneRe.FindAllString(text, -1) {
		digits := 0
		for _, ch := range m {
			if ch >= '0' && ch <= '9' {
				digits++
			}
		}
		if digits >= 8 && digits <= 15 {
			return strings.TrimSpace(m)
		}
	}
	return ""
}

func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// --- Firecrawl API wire types ---

type searchAPIRequest struct {
	Query    string       `json:"query"`
	Limit    int          `json:"limit"`
	Location string       `json:"location,omitempty"`
	Sources  []sourceSpec `json:"sources,omitempty"`
}

type sourceSpec struct {
	Type string `json:"type"`
}

type searchAPIResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Data    struct {
		Web []webResult `json:"web"`
	} `json:"data"`
}

type webResult struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type scrapeAPIRequest struct {
	URL     string       `json:"url"`
	Formats []formatSpec `json:"formats"`
}

type formatSpec struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Prompt string          `json:"prompt,omitempty"`
}

type scrapeAPIResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Data    struct {
		JSON     extractedCompany `json:"json"`
		Markdown string           `json:"markdown"`
	} `json:"data"`
}

type extractedCompany struct {
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Website  string `json:"website"`
	Location string `json:"location"`
}
