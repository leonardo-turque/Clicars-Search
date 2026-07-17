package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/zennitex/clicars-search/internal/domain"
)

// Google Maps DOM selectors — these may need adjustment if Google changes its markup.
const (
	selResultsFeed  = `div[role="feed"]`
	selPlaceLink    = `div[role="feed"] a[href*="/maps/place/"]`
	selPlaceName    = `h1.DUwDvf`
	selAddressBtn   = `button[data-item-id="address"]`
	selPhoneBtn     = `button[data-item-id^="phone:tel:"]`
	selWebsiteLink  = `a[data-item-id="authority"]`
	selInnerText    = `.Io6YTe`

	mapsSearchURL   = "https://www.google.com/maps/search/"
	pageLoadTimeout = 20 * time.Second
	scrollPause     = 1200 * time.Millisecond
	maxScrolls      = 80 // safety cap per grid cell (~20 results each)

	// Google GDPR consent page (shown on EU-hosted servers)
	consentDomain       = "consent.google.com"
	selConsentAcceptAll = `[jsname="b3VHJd"]` // "Accept all" button
)

// GoogleMapsRepository scrapes Google Maps directly (no API key) and implements
// usecase.CompanyFinder.
type GoogleMapsRepository struct {
	pool        *browserPool
	concurrency int
	scrollTO    time.Duration
}

// Option configures a GoogleMapsRepository.
type Option func(*GoogleMapsRepository)

func WithBrowserPool(size int) Option {
	return func(r *GoogleMapsRepository) {
		if size > 0 {
			r.pool = newBrowserPool(size)
		}
	}
}

func WithConcurrency(n int) Option {
	return func(r *GoogleMapsRepository) {
		if n > 0 {
			r.concurrency = n
		}
	}
}

func WithScrollTimeout(d time.Duration) Option {
	return func(r *GoogleMapsRepository) {
		if d > 0 {
			r.scrollTO = d
		}
	}
}

func NewGoogleMapsRepository(opts ...Option) *GoogleMapsRepository {
	r := &GoogleMapsRepository{
		pool:        newBrowserPool(3),
		concurrency: 10,
		scrollTO:    30 * time.Second,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// SearchCompanies implements usecase.CompanyFinder. It discovers places on
// Google Maps for the given niche+location and enriches each with full details.
// onProgress is called with a running total of enriched companies (may be nil).
func (r *GoogleMapsRepository) SearchCompanies(ctx context.Context, niche, location string, quantity int, onProgress func(int)) ([]domain.Company, error) {
	urls, err := r.discoverURLs(ctx, niche, location, quantity)
	if err != nil {
		return nil, fmt.Errorf("googlemaps discover: %w", err)
	}
	if len(urls) == 0 {
		return []domain.Company{}, nil
	}

	companies := r.enrichAll(ctx, urls, onProgress)
	return dedup(companies), nil
}

// Close shuts down all browser instances in the pool.
func (r *GoogleMapsRepository) Close() {
	r.pool.close()
}

// -------------------------------------------------------------------
// Phase 1 — Discovery
// -------------------------------------------------------------------

// discoverURLs collects up to `quantity` Google Maps place URLs for the query.
// For quantities > 60 it subdivides the location into a geographic grid and
// runs multiple searches in parallel, one per grid cell.
func (r *GoogleMapsRepository) discoverURLs(ctx context.Context, niche, location string, quantity int) ([]string, error) {
	if quantity <= 60 {
		return r.discoverSingle(ctx, niche, location, quantity)
	}
	return r.discoverGrid(ctx, niche, location, quantity)
}

// discoverSingle runs one Maps search and scrolls until enough URLs are found.
func (r *GoogleMapsRepository) discoverSingle(ctx context.Context, niche, location string, want int) ([]string, error) {
	b, err := r.pool.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("browser unavailable: %w", err)
	}
	defer r.pool.put(b)

	page, err := newPage(ctx, b)
	if err != nil {
		return nil, err
	}
	defer page.MustClose()

	query := url.PathEscape(niche + " " + location)
	if err := page.Navigate(mapsSearchURL + query); err != nil {
		return nil, fmt.Errorf("navigate maps: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("wait load: %w", err)
	}
	if err := acceptConsent(ctx, page); err != nil {
		log.Printf("googlemaps: consent handling failed (non-fatal): %v", err)
	}

	return r.scrollCollect(ctx, page, want), nil
}

// discoverGrid divides the location's approximate bounding box into cells and
// runs one search per cell in parallel, bounded by the browser pool size.
func (r *GoogleMapsRepository) discoverGrid(ctx context.Context, niche, location string, quantity int) ([]string, error) {
	// Number of grid cells needed (each cell contributes ~40 results on average).
	cellsNeeded := int(math.Ceil(float64(quantity) / 40.0))
	grid := buildGrid(location, cellsNeeded)

	type result struct {
		urls []string
		err  error
	}
	ch := make(chan result, len(grid))

	// Limit parallel grid searches to the pool size.
	sem := make(chan struct{}, r.pool.size())
	var wg sync.WaitGroup

	for _, cell := range grid {
		cell := cell
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				ch <- result{err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			urls, err := r.discoverSingle(ctx, niche, cell, 60)
			ch <- result{urls: urls, err: err}
		}()
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	seen := make(map[string]struct{}, quantity)
	var all []string
	for res := range ch {
		if res.err != nil {
			log.Printf("googlemaps grid cell error (non-fatal): %v", res.err)
			continue
		}
		for _, u := range res.urls {
			if _, ok := seen[u]; !ok {
				seen[u] = struct{}{}
				all = append(all, u)
			}
		}
		if len(all) >= quantity {
			break
		}
	}

	if len(all) > quantity {
		all = all[:quantity]
	}
	return all, nil
}

// scrollCollect scrolls the results panel, collecting place detail URLs until
// `want` are gathered or no new results appear.
func (r *GoogleMapsRepository) scrollCollect(ctx context.Context, page *rod.Page, want int) []string {
	seen := make(map[string]struct{}, want)
	var urls []string

	scrollTO := r.scrollTO
	if scrollTO <= 0 {
		scrollTO = 30 * time.Second
	}

	for scroll := 0; scroll < maxScrolls && len(urls) < want; scroll++ {
		links, err := page.Timeout(scrollTO).Elements(selPlaceLink)
		if err != nil {
			break
		}
		before := len(urls)
		for _, link := range links {
			href, _ := link.Attribute("href")
			if href == nil || *href == "" {
				continue
			}
			clean := canonicalMapsURL(*href)
			if _, dup := seen[clean]; !dup {
				seen[clean] = struct{}{}
				urls = append(urls, clean)
			}
			if len(urls) >= want {
				break
			}
		}

		// If no new results were added after scrolling, we've reached the end.
		if len(urls) == before && scroll > 0 {
			break
		}

		// Scroll the feed panel down to trigger lazy loading.
		_, _ = page.Eval(`() => {
			const feed = document.querySelector('div[role="feed"]');
			if (feed) feed.scrollBy(0, 2000);
		}`)

		select {
		case <-ctx.Done():
			return urls
		case <-time.After(scrollPause):
		}
	}
	return urls
}

// -------------------------------------------------------------------
// Phase 2 — Enrichment
// -------------------------------------------------------------------

// enrichAll visits each place URL in parallel (bounded by r.concurrency) and
// extracts full details. Failures degrade gracefully to partial data.
// onProgress is called with the running total of completed enrichments (may be nil).
func (r *GoogleMapsRepository) enrichAll(ctx context.Context, urls []string, onProgress func(int)) []domain.Company {
	companies := make([]domain.Company, len(urls))
	sem := make(chan struct{}, r.concurrency)
	var wg sync.WaitGroup
	var done atomic.Int64

	for i, u := range urls {
		i, u := i, u
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			b, err := r.pool.get(ctx)
			if err != nil {
				log.Printf("googlemaps: browser unavailable for %q: %v", u, err)
				return
			}
			defer r.pool.put(b)

			c, err := r.extractPlace(ctx, b, u)
			if err != nil {
				log.Printf("googlemaps: enriching %q failed: %v", u, err)
				return
			}
			companies[i] = c

			n := int(done.Add(1))
			if onProgress != nil {
				onProgress(n)
			}
		}()
	}
	wg.Wait()
	return companies
}

// extractPlace opens the place detail page and extracts all available fields.
func (r *GoogleMapsRepository) extractPlace(ctx context.Context, b *rod.Browser, placeURL string) (domain.Company, error) {
	page, err := newPage(ctx, b)
	if err != nil {
		return domain.Company{}, err
	}
	defer page.MustClose()

	if err := page.Navigate(placeURL); err != nil {
		return domain.Company{}, fmt.Errorf("navigate place: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return domain.Company{}, fmt.Errorf("wait load: %w", err)
	}
	if err := acceptConsent(ctx, page); err != nil {
		log.Printf("googlemaps: consent handling in extractPlace (non-fatal): %v", err)
	}

	c := domain.Company{Website: placeURL}

	// Name
	if el, err := page.Timeout(pageLoadTimeout).Element(selPlaceName); err == nil {
		if t, err := el.Text(); err == nil {
			c.Name = strings.TrimSpace(t)
		}
	}

	// Address
	if el, err := page.Timeout(5 * time.Second).Element(selAddressBtn); err == nil {
		if inner, err := el.Element(selInnerText); err == nil {
			if t, err := inner.Text(); err == nil {
				c.Location = strings.TrimSpace(t)
			}
		}
	}

	// Phone
	if el, err := page.Timeout(5 * time.Second).Element(selPhoneBtn); err == nil {
		if inner, err := el.Element(selInnerText); err == nil {
			if t, err := inner.Text(); err == nil {
				c.Phone = strings.TrimSpace(t)
			}
		}
	}

	// Website
	if el, err := page.Timeout(5 * time.Second).Element(selWebsiteLink); err == nil {
		if href, err := el.Attribute("href"); err == nil && href != nil {
			c.Website = strings.TrimSpace(*href)
		}
	}

	return c, nil
}

// -------------------------------------------------------------------
// GDPR consent handling
// -------------------------------------------------------------------

// acceptConsent detects the Google cookie-consent page (consent.google.com)
// that appears when the server is hosted in an EU country, and clicks
// "Accept all" so the browser is redirected back to the intended Maps URL.
// It is a no-op when not on the consent page, so it is safe to call unconditionally.
func acceptConsent(ctx context.Context, page *rod.Page) error {
	info, err := page.Info()
	if err != nil || !strings.Contains(info.URL, consentDomain) {
		return nil
	}

	log.Printf("googlemaps: GDPR consent page detected (%s), accepting cookies", info.URL)

	btn, err := page.Timeout(8 * time.Second).Element(selConsentAcceptAll)
	if err != nil {
		// Fallback: try the last button inside a form pointing to /save
		btn, err = page.Timeout(3 * time.Second).Element(`form[action*="save"] button:last-of-type`)
		if err != nil {
			return fmt.Errorf("consent accept button not found: %w", err)
		}
	}

	if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("consent click failed: %w", err)
	}

	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("wait after consent: %w", err)
	}

	log.Printf("googlemaps: consent accepted, proceeding to Maps")
	return nil
}

// -------------------------------------------------------------------
// Browser pool
// -------------------------------------------------------------------

type browserPool struct {
	ch   chan *rod.Browser
	n    int
	mu   sync.Mutex
	made int
}

// newBrowserPool creates a LAZY pool: browsers are launched on first use, never at
// construction. This keeps the API process alive even when Chromium can't start —
// only the scraping endpoints fail (returning an error), not the whole server,
// nor the WhatsApp/campaign/history routes that don't need a browser.
func newBrowserPool(size int) *browserPool {
	if size <= 0 {
		size = 1
	}
	return &browserPool{ch: make(chan *rod.Browser, size), n: size}
}

// get returns an idle browser, lazily launching a new one while under capacity,
// otherwise blocking until one is returned (or ctx is cancelled). A launch
// failure is surfaced as an error instead of panicking.
func (bp *browserPool) get(ctx context.Context) (*rod.Browser, error) {
	select {
	case b := <-bp.ch:
		return b, nil
	default:
	}

	bp.mu.Lock()
	if bp.made < bp.n {
		bp.made++
		bp.mu.Unlock()
		b, err := newBrowser()
		if err != nil {
			bp.mu.Lock()
			bp.made--
			bp.mu.Unlock()
			return nil, err
		}
		return b, nil
	}
	bp.mu.Unlock()

	select {
	case b := <-bp.ch:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (bp *browserPool) put(b *rod.Browser) {
	if b == nil {
		return
	}
	bp.ch <- b
}

func (bp *browserPool) size() int { return bp.n }

// close drains and shuts down every idle browser. It never blocks waiting for
// in-flight browsers (those are reclaimed by the OS on process exit).
func (bp *browserPool) close() {
	for {
		select {
		case b := <-bp.ch:
			_ = b.Close()
		default:
			return
		}
	}
}

// browserBin resolves the Chromium/Chrome executable. It prefers ROD_BROWSER_BIN
// (set in the Docker image) and falls back to the usual system locations, so rod
// never downloads its own (glibc) build — which can't run on Alpine (musl) and was
// the original cause of the boot-time panic.
func browserBin() string {
	if bin := os.Getenv("ROD_BROWSER_BIN"); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}
	for _, p := range []string{
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "" // let rod manage/download a browser (local dev on glibc)
}

func newBrowser() (*rod.Browser, error) {
	l := launcher.New().
		Headless(true).
		// Anti-detection
		Set("disable-blink-features", "AutomationControlled").
		// Required in container environments
		Set("no-sandbox", "").
		Set("disable-dev-shm-usage", "").
		Set("disable-gpu", "").
		// ── Memory-saving flags ──────────────────────────────────────────
		// Kill features that consume RAM but are useless for scraping.
		Set("disable-background-networking", "").
		Set("disable-background-timer-throttling", "").
		Set("disable-backgrounding-occluded-windows", "").
		Set("disable-breakpad", "").
		Set("disable-client-side-phishing-detection", "").
		Set("disable-component-extensions-with-background-pages", "").
		Set("disable-default-apps", "").
		Set("disable-extensions", "").
		Set("disable-hang-monitor", "").
		Set("disable-ipc-flooding-protection", "").
		Set("disable-popup-blocking", "").
		Set("disable-prompt-on-repost", "").
		Set("disable-renderer-backgrounding", "").
		Set("disable-sync", "").
		Set("disable-translate", "").
		Set("metrics-recording-only", "").
		Set("mute-audio", "").
		Set("no-first-run", "").
		Set("safebrowsing-disable-auto-update", "").
		// Limit disk/media caches to 1 byte (effectively off)
		Set("disk-cache-size", "1").
		Set("media-cache-size", "1").
		// Cap V8 heap per renderer process to 256 MB
		Set("js-flags", "--max-old-space-size=256")

	if bin := browserBin(); bin != "" {
		l = l.Bin(bin)
	}

	u, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("launch chromium: %w", err)
	}

	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("connect chromium: %w", err)
	}
	return browser, nil
}

func newPage(ctx context.Context, b *rod.Browser) (*rod.Page, error) {
	page, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil, fmt.Errorf("new page: %w", err)
	}
	_ = page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
	})
	return page.Context(ctx), nil
}

// -------------------------------------------------------------------
// Geographic grid
// -------------------------------------------------------------------

// gridCell represents a sub-area search query derived from a larger location.
type gridCell = string

// buildGrid generates `n` sub-queries for the given location by appending
// known Brazilian city-level subdivisions (bairros/zonas). For non-Brazilian
// locations or when n is small it returns the original location padded.
func buildGrid(location string, n int) []gridCell {
	suffixes := []string{
		"Centro", "Norte", "Sul", "Leste", "Oeste",
		"zona norte", "zona sul", "zona leste", "zona oeste", "zona central",
		"bairro alto", "bairro baixo", "região central", "região norte", "região sul",
		"região leste", "região oeste", "periferia", "área central", "entorno",
	}

	cells := make([]gridCell, 0, n)
	cells = append(cells, location) // first cell is the bare location

	for i := 0; len(cells) < n && i < len(suffixes); i++ {
		cells = append(cells, location+" "+suffixes[i])
	}

	// If we still need more cells, repeat cycling through suffixes with a numeric tag.
	for extra := 1; len(cells) < n; extra++ {
		cells = append(cells, fmt.Sprintf("%s %s %d", location, suffixes[extra%len(suffixes)], extra))
	}
	return cells[:n]
}

// -------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------

// canonicalMapsURL strips query params and fragment from a Maps place URL so
// the same place reached through different referrers collapses to one entry.
func canonicalMapsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// dedup removes companies with identical normalised name+location pairs and
// filters out entirely empty records.
func dedup(companies []domain.Company) []domain.Company {
	seen := make(map[[32]byte]struct{}, len(companies))
	out := make([]domain.Company, 0, len(companies))
	for _, c := range companies {
		if c.Name == "" && c.Location == "" && c.Phone == "" && c.Website == "" {
			continue
		}
		key := sha256.Sum256([]byte(normalize(c.Name) + "|" + normalize(c.Location)))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}

// normalize folds to lower-case ASCII for deduplication purposes.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}
