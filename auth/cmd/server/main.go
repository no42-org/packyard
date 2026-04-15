package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/no42-org/packyard-auth/internal/handler"
	"github.com/no42-org/packyard-auth/internal/metrics"
	"github.com/no42-org/packyard-auth/internal/middleware"
	"github.com/no42-org/packyard-auth/internal/store"
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

	validComponents, publicComponents, err := st.LoadComponentSets(context.Background())
	if err != nil {
		logger.Error("failed to load components from database", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Build sorted component list string for error messages.
	names := make([]string, 0, len(validComponents))
	for name := range validComponents {
		names = append(names, name)
	}
	sort.Strings(names)
	componentList := strings.Join(names, ", ")

	// Build visibility map for key response wrapping.
	compVisibility := make(map[string]string, len(validComponents))
	for name := range validComponents {
		if publicComponents[name] {
			compVisibility[name] = "public"
		} else {
			compVisibility[name] = "private"
		}
	}

	logger.Info("loaded components from database", slog.String("components", componentList))

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.RequestLogger(logger))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	forwardAuth := handler.NewForwardAuthHandler(st, logger, validComponents, publicComponents)
	r.Get("/auth", forwardAuth.ServeHTTP)

	keys := handler.NewKeysHandler(st, logger, validComponents, componentList, compVisibility)
	r.Post("/api/v1/keys", keys.Create)
	r.Get("/api/v1/keys", keys.List)
	r.Get("/api/v1/keys/{id}", keys.Get)
	r.Delete("/api/v1/keys/{id}", keys.Delete)

	rpmDataRoot := os.Getenv("RPM_DATA_ROOT")
	components := handler.NewComponentsHandler(st, logger, rpmDataRoot)
	r.Post("/api/v1/components", components.Create)
	r.Get("/api/v1/components", components.List)
	r.Get("/api/v1/components/{name}", components.GetOne)
	r.Delete("/api/v1/components/{name}", components.Delete)

	// Metrics server on :9090 — internal Docker network only, not published to host.
	_ = metrics.RequestsTotal   // ensure package init() runs before the server starts
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(":9090", mux); err != nil {
			logger.Error("metrics server error", slog.String("error", err.Error()))
		}
	}()

	logger.Info("starting packyard-auth", slog.String("version", version), slog.String("addr", ":8080"), slog.String("db", dbPath))
	if err := http.ListenAndServe(":8080", r); err != nil {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
