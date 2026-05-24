package middleware

import (
	"net/http"
	"strings"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
)

// nonGETAllowListForReadonly enumerates paths where a readonly operator is
// allowed to issue a non-GET request. Per D24 only the logout endpoint is on
// this list — readonly operators must be able to log themselves out.
var nonGETAllowListForReadonly = map[string]bool{
	"/api/v1/auth/logout": true,
}

// RoleConfig wires the role middleware to its audit sink. Auditor receives a
// `auth.role_denied` row on every 403 ROLE_DENIED rejection per
// operator-authn spec § "Audit log coverage".
type RoleConfig struct {
	Auditor audit.Auditor
}

// RequireRole returns a middleware that blocks non-GET requests for readonly
// operators with 403 ROLE_DENIED. GETs (read-only access) are allowed for any
// authenticated operator. The middleware MUST be wired AFTER RequireSession
// so an operator is present in the request context; if no operator has been
// injected at all, the middleware fails closed with 401 UNAUTHORIZED rather
// than defaulting to admin.
//
// The trailing slash is stripped from the request path before consulting the
// non-GET allow-list so `/api/v1/auth/logout/` is treated the same as
// `/api/v1/auth/logout` (D24).
//
// Every 403 ROLE_DENIED denial emits an `auth.role_denied` audit row with the
// rejected operator id, method, and path so admins can spot probing.
func RequireRole(cfg RoleConfig) func(http.Handler) http.Handler {
	auditor := cfg.Auditor
	if auditor == nil {
		auditor = audit.NoopAuditor{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			path := strings.TrimRight(r.URL.Path, "/")
			if path == "" {
				path = "/"
			}
			if nonGETAllowListForReadonly[path] {
				next.ServeHTTP(w, r)
				return
			}
			op, ok := auth.OperatorFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
					"no authenticated operator on the request")
				return
			}
			if op.Role == auth.RoleReadonly {
				audit.WriteFromRequest(r.Context(), auditor, r, audit.Entry{
					OperatorID: op.ID,
					Action:     "auth.role_denied",
					TargetType: "endpoint",
					// Operator-controllable; bound the audit cost the same
					// way the rate-limiter bounds its denial-path field.
					TargetID: TruncateAuditField(r.URL.Path),
					Details: map[string]any{
						"method": r.Method,
						"role":   string(op.Role),
					},
				})
				writeError(w, http.StatusForbidden, "ROLE_DENIED",
					"this endpoint requires the admin role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
