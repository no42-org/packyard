// Package auth carries operator identity through request context.
//
// Until the OAuth + session middleware (change 2026-05-21-admin-ui-account-lifecycle § 4)
// lands, handlers that need an operator identity get the bootstrap stub from
// OperatorFromContext. Once the middleware is wired, it will inject a real
// Operator via WithOperator at the start of every authenticated request and
// the call sites here do not change.
package auth

import "context"

// Role enumerates the operator role values that match the operators.role
// CHECK constraint in the SQLite schema.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleReadonly Role = "readonly"
)

// Operator is the authenticated principal for an admin-API request.
type Operator struct {
	ID    string
	Email string
	Role  Role
}

// IsAdmin returns true when the operator may perform mutating operations.
func (o Operator) IsAdmin() bool { return o.Role == RoleAdmin }

type contextKey struct{}

// operatorKey is the context key for the request operator.
var operatorKey = contextKey{}

// WithOperator returns a copy of ctx that carries op as the request operator.
// The session middleware calls this once per authenticated request. Tests use
// it to simulate role gates.
func WithOperator(ctx context.Context, op Operator) context.Context {
	return context.WithValue(ctx, operatorKey, op)
}

// OperatorFromContext returns the operator attached to ctx and a boolean
// indicating whether one was actually injected. Callers that need a role
// decision MUST consult the bool — a zero Operator has an empty Role that
// is neither admin nor readonly, so role checks would silently fail-open if
// the caller assumed an operator was present.
//
// The session middleware is the only production path that injects an
// operator; if it didn't run (mis-wired router, test bypass) the bool is
// false and the request should be rejected.
func OperatorFromContext(ctx context.Context) (Operator, bool) {
	op, ok := ctx.Value(operatorKey).(Operator)
	return op, ok
}
