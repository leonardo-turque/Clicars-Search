package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zennitex/clicars-search/internal/campaign"
	deliveryhttp "github.com/zennitex/clicars-search/internal/delivery/http"
	"github.com/zennitex/clicars-search/internal/protect"
	"github.com/zennitex/clicars-search/internal/repository"
	"github.com/zennitex/clicars-search/internal/usecase"
	"github.com/zennitex/clicars-search/internal/whatsapp"
)

func main() {
	dsn := buildDSN()
	pool := mustConnect(dsn)
	defer pool.Close()
	log.Println("database connection established")

	// --- Dependency wiring (Clean Architecture) ---
	gmaps := repository.NewGoogleMapsRepository(
		repository.WithBrowserPool(envInt("GOOGLEMAPS_BROWSER_POOL", 3)),
		repository.WithConcurrency(envInt("GOOGLEMAPS_CONCURRENCY", 10)),
		repository.WithScrollTimeout(envDuration("GOOGLEMAPS_SCROLL_TIMEOUT", 30*time.Second)),
	)
	store := repository.NewSearchRepository(pool)
	campaignRepo := repository.NewCampaignRepository(pool)
	if err := campaignRepo.EnsureSchema(context.Background()); err != nil {
		log.Fatalf("campaign schema: %v", err)
	}
	performSearch := usecase.NewPerformSearch(gmaps, store)
	searchHandler := deliveryhttp.NewSearchHandler(performSearch, store)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	searchHandler.RegisterRoutes(mux)

	// --- WhatsApp via hosted Zennitex API ---
	// Optional: without WHATSAPP_API_URL + WHATSAPP_ADMIN_KEY the core search API
	// still comes up; only WhatsApp routes and campaign dispatch are skipped.
	var dispatcher *campaign.Dispatcher
	var waManager *whatsapp.Manager
	var protectEngine *protect.Engine
	waManager, waErr := whatsapp.NewManager(
		getEnv("WHATSAPP_API_URL", ""),
		getEnv("WHATSAPP_ADMIN_KEY", ""),
		log.Default(),
	)
	if waErr != nil {
		log.Printf("whatsapp: disabled (%v)", waErr)
	} else {
		waHandler := deliveryhttp.NewWhatsAppHandler(waManager)
		waHandler.RegisterRoutes(mux)

		protectStore := protect.NewStore(pool)
		if err := protectStore.EnsureSchema(context.Background()); err != nil {
			log.Fatalf("protect schema: %v", err)
		}
		lunchFrom, lunchTo := 12, 13
		if strings.EqualFold(getEnv("PROTECT_LUNCH_PAUSE", "on"), "off") {
			lunchFrom, lunchTo = 0, 0
		}
		protectEngine = protect.NewEngine(protectStore, protect.Config{
			Timezone:          getEnv("PROTECT_TIMEZONE", "America/Sao_Paulo"),
			BusinessStartHour: envInt("PROTECT_START_HOUR", 8),
			BusinessEndHour:   envInt("PROTECT_END_HOUR", 20),
			LunchFrom:         lunchFrom,
			LunchTo:           lunchTo,
			DailyTarget:       envInt("PROTECT_DAILY_TARGET", 200),
			MinDelay:          envDuration("PROTECT_MIN_DELAY", 90*time.Second),
			MaxDelay:          envDuration("PROTECT_MAX_DELAY", 4*time.Minute),
			RestEvery:         envInt("PROTECT_REST_EVERY", 10),
			RestMin:           envDuration("PROTECT_REST_MIN", 4*time.Minute),
			RestMax:           envDuration("PROTECT_REST_MAX", 12*time.Minute),
			WeekendFactor:     0.55,
			MaturePhones:      envCSV("WARMUP_MATURE_PHONES", nil),
			PrimaryPhones:     envCSV("WARMUP_PRIMARY_PHONES", []string{protect.PrimaryPhone}),
			WarmupInterval:    envDuration("WARMUP_INTERVAL", 8*time.Minute),
			WarmupTick:        envDuration("WARMUP_TICK", 3*time.Minute),
		}, log.Default(), nil)
		protectEngine.Attach(waManager, waManager)
		deliveryhttp.NewProtectHandler(protectEngine).RegisterRoutes(mux)

		dispatcher = campaign.NewDispatcherWithConfig(
			campaignRepo, campaignRepo, waManager, log.Default(),
			campaign.Config{
				MinDelay:    envDuration("CAMPAIGN_MIN_DELAY", 90*time.Second),
				MaxDelay:    envDuration("CAMPAIGN_MAX_DELAY", 4*time.Minute),
				RatePerHour: envInt("CAMPAIGN_RATE_PER_HOUR", 20),
				MaxWorkers:  envInt("CAMPAIGN_MAX_WORKERS", 5),
				Guard:       protectEngine,
			},
		)
		deliveryhttp.NewCampaignHandler(dispatcher).RegisterRoutes(mux)

		go func() {
			protectEngine.Start()
			dispatcher.Start()
		}()
		log.Printf("whatsapp: enabled via %s (max %d sessions)",
			getEnv("WHATSAPP_API_URL", ""), whatsapp.MaxSessions)
		log.Printf("protect: anti-ban + warmup enabled (target %d/day, primary=%s)",
			envInt("PROTECT_DAILY_TARGET", 200),
			strings.Join(envCSV("WARMUP_PRIMARY_PHONES", []string{protect.PrimaryPhone}), ","),
		)
	}

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           corsMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// --- Retention worker (runs every 24h, deletes searches older than 45 days) ---
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		log.Println("retention worker: started (45-day policy)")
		for {
			select {
			case <-ticker.C:
				if c, err := campaignRepo.DeleteOldCampaigns(workerCtx, 45); err != nil {
					log.Printf("retention worker: campaign cleanup error: %v", err)
				} else if c > 0 {
					log.Printf("retention worker: removed %d campaigns older than 45 days", c)
				}
				n, err := store.DeleteOldSearches(workerCtx, 45)
				if err != nil {
					log.Printf("retention worker: cleanup error: %v", err)
				} else {
					log.Printf("retention worker: removed %d searches older than 45 days", n)
				}
			case <-workerCtx.Done():
				log.Println("retention worker: stopped")
				return
			}
		}
	}()

	go func() {
		log.Printf("server listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// --- Graceful shutdown ---
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down server...")

	if dispatcher != nil {
		dispatcher.Shutdown(10 * time.Second)
	}
	if protectEngine != nil {
		protectEngine.Shutdown(5 * time.Second)
	}
	if waManager != nil {
		waManager.Shutdown()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}
	log.Println("server stopped")
}

func mustConnect(dsn string) *pgxpool.Pool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("unable to create connection pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("unable to reach database: %v", err)
	}
	return pool
}

func buildDSN() string {
	host := getEnv("DB_HOST", "localhost")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "clicars")
	password := getEnv("DB_PASSWORD", "clicars_pass")
	dbname := getEnv("DB_NAME", "clicars_search")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s", user, password, host, port, dbname)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscan(v, &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envCSV(key string, fallback []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
