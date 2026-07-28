package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zennitex/clicars-search/internal/campaign"
	deliveryhttp "github.com/zennitex/clicars-search/internal/delivery/http"
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

		dispatcher = campaign.NewDispatcherWithConfig(
			campaignRepo, campaignRepo, waManager, log.Default(),
			campaign.Config{
				MinDelay:    envDuration("CAMPAIGN_MIN_DELAY", 30*time.Second),
				MaxDelay:    envDuration("CAMPAIGN_MAX_DELAY", 90*time.Second),
				RatePerHour: envInt("CAMPAIGN_RATE_PER_HOUR", 30),
				MaxWorkers:  envInt("CAMPAIGN_MAX_WORKERS", 5),
			},
		)
		deliveryhttp.NewCampaignHandler(dispatcher).RegisterRoutes(mux)

		go func() {
			// Remote sessions reconnect on the WhatsApp API itself; just resume queues.
			dispatcher.Start()
		}()
		log.Printf("whatsapp: enabled via %s (max %d sessions)",
			getEnv("WHATSAPP_API_URL", ""), whatsapp.MaxSessions)
		log.Printf("campaigns: enabled (durable queue: %s–%s delay, %d/h per number)",
			envDuration("CAMPAIGN_MIN_DELAY", 30*time.Second),
			envDuration("CAMPAIGN_MAX_DELAY", 90*time.Second),
			envInt("CAMPAIGN_RATE_PER_HOUR", 30),
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
