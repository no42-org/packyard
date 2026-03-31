package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/opennms/packyard-auth/internal/handler"
	"github.com/opennms/packyard-auth/internal/metrics"
	"github.com/opennms/packyard-auth/internal/middleware"
	"github.com/opennms/packyard-auth/internal/store"
)

func main() {
	// Health-check mode: invoked by Docker Compose healthcheck test.
	// Performs a GET /health against the running server and exits 0 on success.
	if len(os.Args) > 1 && os.Args[1] == "-health-check" {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://localhost:8080/health")
		if err != nil {
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			os.Exit(0)
		}
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/db/auth.db"
	}

	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		logger.Error("failed to open store", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer st.Close()

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.RequestLogger(logger))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	forwardAuth := &handler.ForwardAuthHandler{
		Store:  st,
		Logger: logger,
	}
	r.Get("/auth", forwardAuth.ServeHTTP)

	keys := &handler.KeysHandler{
		Store:  st,
		Logger: logger,
	}
	r.Post("/api/v1/keys", keys.Create)
	r.Get("/api/v1/keys", keys.List)
	r.Get("/api/v1/keys/{id}", keys.Get)
	r.Delete("/api/v1/keys/{id}", keys.Delete)

	// Metrics server on :9090 — internal Docker network only, not published to host.
	_ = metrics.RequestsTotal   // ensure package init() runs before the server starts
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(":9090", mux); err != nil {
			logger.Error("metrics server error", slog.String("error", err.Error()))
		}
	}()

	logger.Info("starting packyard-auth", slog.String("addr", ":8080"), slog.String("db", dbPath))
	if err := http.ListenAndServe(":8080", r); err != nil {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
