package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/no42-org/packyard-auth/internal/adminui"
	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/auth/github"
	"github.com/no42-org/packyard-auth/internal/auth/microsoft"
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

	// § 5.4: env-var bootstrap of the first operator. Idempotent and inert
	// once any operator exists.
	if op, err := st.BootstrapOperatorFromEnv(context.Background(), os.Getenv("PACKYARD_BOOTSTRAP_OPERATOR_EMAIL")); err != nil {
		logger.Error("bootstrap operator failed", slog.String("error", err.Error()))
		os.Exit(1)
	} else if op != nil {
		logger.Info("bootstrap operator created",
			slog.String("operator_id", op.ID),
			slog.String("email", op.Email))
	}

	sessionMW := middleware.RequireSession(middleware.SessionConfig{
		Sessions:  st,
		Operators: st,
		Logger:    logger,
	})

	// OAuth providers — registered only when the COMPLETE env config is
	// present for that provider. Partial config (e.g. CLIENT_ID without
	// CLIENT_SECRET) fails the startup rather than registering a half-baked
	// provider that surfaces confusing errors at first login.
	providers := map[string]auth.OAuthProvider{}
	if gh := buildGitHubProvider(logger); gh != nil {
		providers[auth.ProviderGitHub] = gh
	}
	if ms := buildMicrosoftProvider(logger); ms != nil {
		providers[auth.ProviderMicrosoft] = ms
	}

	// OAuth state store auto-starts its reaper; lifetime is tied to the
	// server context so the goroutine exits when the server shuts down.
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()
	stateStore := auth.NewMemStateStore(serverCtx, 5*time.Minute)

	// Auditor: the SQLiteStore implements audit.Auditor via its Write method,
	// so all audit calls now persist to the audit_log table. The store
	// internally logs persistence failures (fire-and-forget per the Auditor
	// contract).
	st.SetAuditLogger(logger)
	var auditor audit.Auditor = st

	// Rate limiter on the OAuth flow per D21.
	rateLimiter := middleware.NewRateLimiter(serverCtx, middleware.RateLimiterConfig{
		Auditor: auditor,
		Logger:  logger,
	})

	// CSRF guard on mutating /api/v1/* requests per D15. The admin host is
	// the public hostname (§ 7) — required in production. Bare requirement
	// here panics on missing config; ops sets PACKYARD_ADMIN_HOST.
	//
	// The value must be a bare scheme://host[:port] origin — anything else
	// (extra path, trailing slash, double scheme from ADMIN_DOMAIN already
	// containing 'https://') yields a URL that parseOrigin accepts but that
	// will never match a real browser Origin, silently 403'ing every
	// mutating request post-deploy. Validate the shape here so operators
	// see the misconfiguration at startup, not in the access log.
	adminHost := os.Getenv("PACKYARD_ADMIN_HOST")
	if adminHost == "" {
		logger.Error("PACKYARD_ADMIN_HOST is required for CSRF enforcement (D15)")
		os.Exit(1)
	}
	if !validAdminHost(adminHost) {
		logger.Error("PACKYARD_ADMIN_HOST must be a bare scheme://host[:port] origin",
			slog.String("got", adminHost),
			slog.String("hint", "ADMIN_DOMAIN should be just the hostname (e.g. admin.pkg.example.org), not a URL"))
		os.Exit(1)
	}
	csrfGuard := middleware.CSRFGuard(adminHost)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.RequestLogger(logger))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	forwardAuth := handler.NewForwardAuthHandler(st, st, st, logger)
	r.Get("/auth", forwardAuth.ServeHTTP)

	authH := handler.NewAuthHandler(st, auditor, logger)
	authH.Operators = st
	authH.Providers = providers
	authH.State = stateStore

	keys := handler.NewKeysHandler(st, st, st, auditor, logger, validComponents, componentList, compVisibility)
	rpmDataRoot := os.Getenv("RPM_DATA_ROOT")
	components := handler.NewComponentsHandler(st, logger, rpmDataRoot)
	accounts := handler.NewAccountsHandler(st, st, auditor, logger, compVisibility)
	auditH := handler.NewAuditHandler(st, logger)
	operators := handler.NewOperatorsHandler(st, st, auditor, logger)

	// Public OAuth entry points — outside the session-protected group per
	// tasks.md § 4.6. /login/* initiates a flow; /callback/* completes it.
	// Rate-limited per D21 to bound abuse of these unauthenticated paths.
	r.Group(func(r chi.Router) {
		r.Use(rateLimiter.Middleware)
		r.Get("/api/v1/auth/login/{provider}", authH.Login)
		r.Get("/api/v1/auth/callback/{provider}", authH.Callback)
	})

	// Every other /api/v1/* admin route requires a valid session. Role
	// enforcement (block non-GET for readonly) runs after session; CSRF
	// guard runs first because it's a pure header check that can short-
	// circuit cross-origin attacks before any auth work.
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(csrfGuard)
		r.Use(sessionMW)
		r.Use(middleware.RequireRole(middleware.RoleConfig{Auditor: auditor}))

		r.Get("/auth/whoami", authH.Whoami)
		r.Post("/auth/logout", authH.Logout)

		r.Post("/keys", keys.Create)
		r.Get("/keys", keys.List)
		r.Get("/keys/{id}", keys.Get)
		r.Delete("/keys/{id}", keys.Delete)

		r.Post("/components", components.Create)
		r.Get("/components", components.List)
		r.Get("/components/{name}", components.GetOne)
		r.Patch("/components/{name}", components.Update)
		r.Delete("/components/{name}", components.Delete)

		r.Post("/accounts", accounts.Create)
		r.Get("/accounts", accounts.List)
		r.Get("/accounts/{id}", accounts.Get)
		r.Patch("/accounts/{id}", accounts.Update)
		r.Delete("/accounts/{id}", accounts.Delete)
		r.Get("/accounts/{id}/keys", accounts.ListKeys)
		r.Post("/accounts/{id}/keys", accounts.IssueKey)

		// § 6.3 audit query — readable by admin AND readonly (GET). No
		// mutation routes exist: rows are immutable via API per § 6.4.
		r.Get("/audit", auditH.List)

		// § 5.2 operator management — admin-only (enforced inside each
		// handler via requireAdmin). No DELETE: an operator is disabled,
		// never removed, to preserve audit trail attribution.
		r.Get("/operators", operators.List)
		r.Post("/operators", operators.Create)
		r.Patch("/operators/{id}", operators.Update)
	})

	// § 7.7 + § 8.1: serve the embedded admin SPA at /admin/* with the per-
	// request CSP nonce in context. Unauthenticated — the SPA itself calls
	// /api/v1/auth/whoami and redirects to /login on 401, so the shell HTML
	// is safe to serve without a session cookie. Asset paths under
	// /admin/assets/* are content-hashed and cacheable.
	r.Route("/admin", func(r chi.Router) {
		r.Use(middleware.AdminCSP)
		r.Mount("/", adminui.Handler(logger))
	})

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

// buildGitHubProvider validates the complete GitHub env-var set and
// constructs the provider. Returns nil (silently) when none of the GitHub
// env vars are set. Logs + exits when the set is partial — half-configured
// is always a mistake.
func buildGitHubProvider(logger *slog.Logger) auth.OAuthProvider {
	id := os.Getenv("PACKYARD_GITHUB_CLIENT_ID")
	secret := os.Getenv("PACKYARD_GITHUB_CLIENT_SECRET")
	redirect := os.Getenv("PACKYARD_GITHUB_REDIRECT_URI")
	org := os.Getenv("PACKYARD_GITHUB_ORG")
	any := id != "" || secret != "" || redirect != "" || org != ""
	all := id != "" && secret != "" && redirect != "" && org != ""
	if !any {
		return nil
	}
	if !all {
		logger.Error("github oauth provider is partially configured; require all of CLIENT_ID/CLIENT_SECRET/REDIRECT_URI/ORG together")
		os.Exit(1)
	}
	logger.Info("oauth provider registered", slog.String("provider", auth.ProviderGitHub))
	return github.New(github.Config{
		ClientID:     id,
		ClientSecret: secret,
		RedirectURI:  redirect,
		AllowedOrg:   org,
	})
}

// buildMicrosoftProvider mirrors buildGitHubProvider for the Microsoft Entra
// provider. Tenant id is the additional required field.
func buildMicrosoftProvider(logger *slog.Logger) auth.OAuthProvider {
	id := os.Getenv("PACKYARD_MICROSOFT_CLIENT_ID")
	secret := os.Getenv("PACKYARD_MICROSOFT_CLIENT_SECRET")
	redirect := os.Getenv("PACKYARD_MICROSOFT_REDIRECT_URI")
	tenant := os.Getenv("PACKYARD_MICROSOFT_TENANT_ID")
	any := id != "" || secret != "" || redirect != "" || tenant != ""
	all := id != "" && secret != "" && redirect != "" && tenant != ""
	if !any {
		return nil
	}
	if !all {
		logger.Error("microsoft oauth provider is partially configured; require all of CLIENT_ID/CLIENT_SECRET/REDIRECT_URI/TENANT_ID together")
		os.Exit(1)
	}
	logger.Info("oauth provider registered", slog.String("provider", auth.ProviderMicrosoft))
	return microsoft.New(microsoft.Config{
		TenantID:     tenant,
		ClientID:     id,
		ClientSecret: secret,
		RedirectURI:  redirect,
	})
}

// validAdminHost returns true if s is a bare scheme://host[:port] origin —
// the shape that CSRFGuard expects. Rejects values with paths, query
// strings, fragments, or extra schemes. Catches the common operator
// footgun of setting ADMIN_DOMAIN=https://admin.example.org (which
// Compose then interpolates into PACKYARD_ADMIN_HOST=https://https://...).
func validAdminHost(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	if u.Path != "" && u.Path != "/" {
		return false
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	// Host must not itself look like a URL (catches https://https://x).
	if strings.Contains(u.Host, ":/") || strings.Contains(u.Host, "/") {
		return false
	}
	return true
}
