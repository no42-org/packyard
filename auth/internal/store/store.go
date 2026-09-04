/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package store

import (
	"context"
	"errors"
	"time"

	"github.com/no42-org/packyard-auth/internal/audit"
)

var (
	ErrNotFound           = errors.New("key not found")
	ErrRevoked            = errors.New("key is revoked")
	ErrComponentExists    = errors.New("component already exists")
	ErrComponentNotFound  = errors.New("component not found")
	ErrAccountNotFound    = errors.New("account not found")
	ErrAccountEmailExists = errors.New("account email already exists")
	// ErrAccountInvalid is returned by the store when an account input
	// violates a CHECK constraint (empty email, non-canonical email,
	// malformed email). Handlers map this to 400 INVALID_REQUEST.
	ErrAccountInvalid = errors.New("account input invalid")
	// ErrInvalidStatusTransition is returned by UpdateAccount when the
	// caller asks for an unsupported status transition (e.g. target='deleted'
	// without going through DELETE).
	ErrInvalidStatusTransition = errors.New("invalid account status transition")
	// ErrLegacyAccountProtected is returned for mutations against the
	// synthetic legacy account that would harm subscriber service.
	ErrLegacyAccountProtected = errors.New("legacy account is protected")
	// ErrOperatorNotFound is returned when no operator row matches the lookup
	// key (id or canonical email).
	ErrOperatorNotFound = errors.New("operator not found")
	// ErrOperatorExists is returned when an Allowlist call would create a
	// duplicate operator email.
	ErrOperatorExists = errors.New("operator email already exists")
	// ErrOperatorInvalid is returned when an operator input violates a CHECK
	// constraint (empty email, non-canonical email, malformed email).
	ErrOperatorInvalid = errors.New("operator input invalid")
	// ErrOperatorSelfLockout is returned by UpdateOperatorAtomically when the
	// requested role/status transition would leave zero active admins. The
	// guard runs inside the same serializable transaction as the mutation so
	// two concurrent demotes cannot both pass.
	ErrOperatorSelfLockout = errors.New("operator update would leave zero active admins")
	// ErrSessionNotFound is returned when a session id does not match any
	// row or its operator has been removed.
	ErrSessionNotFound = errors.New("session not found")
)

// Key represents a subscription key.
type Key struct {
	ID         string     `json:"id"`
	Component  string     `json:"component"`
	Active     bool       `json:"active"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	UsageCount int64      `json:"usage_count"`
	AccountID  string     `json:"account_id"`
}

// KeyStore is the interface for subscription key storage.
// All handler code interacts with this interface, never with a concrete implementation.
type KeyStore interface {
	CreateKey(ctx context.Context, accountID, component, label string, expiresAt *time.Time) (*Key, error)
	GetByValue(ctx context.Context, value string) (*Key, error)
	GetByID(ctx context.Context, id string) (*Key, error)
	// ListKeys returns keys ordered by created_at DESC. Empty filter strings
	// disable that filter; both filters may be combined. The caller passes
	// offset/limit clamped to D23 bounds; the store applies them via SQL
	// `LIMIT ? OFFSET ?`.
	ListKeys(ctx context.Context, componentFilter, accountFilter string, offset, limit int) ([]*Key, error)
	RevokeKey(ctx context.Context, id string) error
	IncrementUsage(ctx context.Context, id string) error
}

// Component represents a provisioned LTS component.
type Component struct {
	Name             string    `json:"name"`
	Visibility       string    `json:"visibility"`
	RPMSeries        []string  `json:"rpm_series"`
	RPMOSFamilies    []string  `json:"rpm_os_families"`
	RPMArchitectures []string  `json:"rpm_architectures"`
	CreatedAt        time.Time `json:"created_at"`
}

// ComponentStore is the interface for component provisioning storage.
type ComponentStore interface {
	CreateComponent(ctx context.Context, comp *Component) (*Component, error)
	GetComponent(ctx context.Context, name string) (*Component, error)
	ListComponents(ctx context.Context, offset, limit int) ([]*Component, error)
	DeleteComponent(ctx context.Context, name string) error
	RevokeComponentKeys(ctx context.Context, component string) (int64, error)
	CountActiveComponentKeys(ctx context.Context, component string) (int64, error)
	DeleteComponentWithRevoke(ctx context.Context, name string) (int64, error)
	UpdateComponentVisibility(ctx context.Context, name, visibility string) (*Component, error)
}

// AccountStatus values that align with the accounts.status CHECK constraint.
type AccountStatus string

const (
	AccountStatusActive    AccountStatus = "active"
	AccountStatusSuspended AccountStatus = "suspended"
	AccountStatusDeleted   AccountStatus = "deleted"
)

// Account is the subscriber identity that owns subscription keys.
type Account struct {
	ID                  string        `json:"id"`
	Email               string        `json:"email"`
	OrgName             string        `json:"org_name"`
	Status              AccountStatus `json:"status"`
	CreatedAt           time.Time     `json:"created_at"`
	CreatedByOperatorID string        `json:"created_by_operator_id"`
}

// AccountInput is the create payload — fields the operator supplies. The id
// is generated by the store; created_at and created_by_operator_id are set
// by the store/caller respectively.
type AccountInput struct {
	Email   string
	OrgName string
}

// AccountUpdate is a partial update for PATCH /api/v1/accounts/{id}.
// nil-valued fields are left unchanged; non-nil fields are applied.
type AccountUpdate struct {
	Email   *string
	OrgName *string
	Status  *AccountStatus
}

// OperatorRole enumerates the operator role values that match the
// operators.role CHECK constraint.
type OperatorRole string

const (
	OperatorRoleAdmin    OperatorRole = "admin"
	OperatorRoleReadonly OperatorRole = "readonly"
)

// OperatorStatus enumerates the operator status values.
type OperatorStatus string

const (
	OperatorStatusActive   OperatorStatus = "active"
	OperatorStatusDisabled OperatorStatus = "disabled"
)

// Operator represents one row of the operators table.
type Operator struct {
	ID                string         `json:"id"`
	Email             string         `json:"email"`
	Role              OperatorRole   `json:"role"`
	Status            OperatorStatus `json:"status"`
	AllowlistedAt     time.Time      `json:"allowlisted_at"`
	AllowlistedBy     string         `json:"allowlisted_by"`
	LastLoginAt       *time.Time     `json:"last_login_at,omitempty"`
	GithubUsername    string         `json:"github_username,omitempty"`
	MicrosoftUPN      string         `json:"microsoft_upn,omitempty"`
	FirstSeenProvider string         `json:"first_seen_provider,omitempty"`
}

// OperatorStore covers the methods used in chunk A of § 4 (session lookup +
// env-var bootstrap). Full § 5 operator management (List/Disable/ChangeRole)
// will extend this interface.
type OperatorStore interface {
	// GetOperator returns the operator by id or ErrOperatorNotFound.
	GetOperator(ctx context.Context, id string) (*Operator, error)
	// GetOperatorByEmail returns the operator whose canonical email matches.
	// Email is canonicalised inside the store; callers pass the raw value.
	GetOperatorByEmail(ctx context.Context, email string) (*Operator, error)
	// AllowlistOperator inserts a new operator and returns the new row. The
	// allowlistedBy is the caller's operator id; for the env-var bootstrap
	// it is "bootstrap" (matching the legacy account seed convention).
	AllowlistOperator(ctx context.Context, email string, role OperatorRole, allowlistedBy string) (*Operator, error)
	// OperatorCount returns the total number of rows in the operators table.
	// Used by the bootstrap path to refuse to insert when any operator exists.
	OperatorCount(ctx context.Context) (int64, error)
	// DisableOperator sets the operator's status to 'disabled'. § 5.2 will
	// wrap this in the operator management handler; chunk A exposes it so
	// the session middleware path for disabled operators is testable.
	DisableOperator(ctx context.Context, id string) error
	// ListOperators returns operators ordered by allowlisted_at DESC.
	// Pagination follows D23 (default 50, max 500); caller clamps.
	ListOperators(ctx context.Context, offset, limit int) ([]*Operator, error)
	// ChangeOperatorRole sets the role; rejects unknown values. Audit
	// `operator.role_change` is emitted at the handler layer, not here.
	ChangeOperatorRole(ctx context.Context, id string, role OperatorRole) error
	// EnableOperator flips status back to 'active' — required for the role
	// change / disable round-trip and for an admin to reactivate a peer.
	EnableOperator(ctx context.Context, id string) error
	// CountActiveAdmins returns the number of operators with role='admin'
	// AND status='active'. Exposed for tests and metrics; the production
	// self-lockout guard lives inside UpdateOperatorAtomically so the
	// count+mutation pair is atomic.
	CountActiveAdmins(ctx context.Context) (int64, error)
	// UpdateOperatorAtomically applies an optional role and/or status change
	// inside a single serializable transaction. The post-state global self-
	// lockout guard runs inside the same transaction: any mutation that
	// removes an active admin and would leave zero active admins returns
	// ErrOperatorSelfLockout and rolls back. Returns the before/after rows so
	// handlers can emit accurate audit transitions (from/to).
	//
	// At least one of newRole / newStatus must be non-nil; passing both nil
	// returns ErrOperatorInvalid. The method runs only one UPDATE statement
	// when both fields are set, so role and status either both commit or
	// neither does.
	UpdateOperatorAtomically(ctx context.Context, id string,
		newRole *OperatorRole, newStatus *OperatorStatus,
	) (before, after *Operator, err error)
	// UpdateLastLogin records the operator's most recent successful OAuth
	// login. Called from the callback success path.
	UpdateLastLogin(ctx context.Context, id string, ts time.Time) error
	// UpdateLoginProvider opportunistically populates the provider-specific
	// identity columns (github_username / microsoft_upn) and
	// first_seen_provider on first OAuth login from a given provider per
	// D14. Idempotent: re-running with the same provider+id is a no-op.
	UpdateLoginProvider(ctx context.Context, id string, providerName, providerUserID string) error
}

// Session is one row of the sessions table per D16.
type Session struct {
	ID         string    `json:"id"`
	OperatorID string    `json:"operator_id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

// SessionStore covers § 4.1: server-side sessions backed by SQLite.
type SessionStore interface {
	// CreateSession allocates a new 32-byte-hex session id and inserts a row.
	// The expires_at is computed from createdAt + the absolute session
	// lifetime (24h per D17). last_seen_at = createdAt.
	CreateSession(ctx context.Context, operatorID, ip, userAgent string) (*Session, error)
	// GetSession returns the session by id or ErrSessionNotFound. The
	// implementation joins against operators so that a session whose
	// operator was deleted resolves as ErrSessionNotFound (FK CASCADE
	// already deletes the session row, but a future race or schema change
	// is guarded by this contract).
	GetSession(ctx context.Context, id string) (*Session, error)
	// TouchSession updates last_seen_at to now. Returns ErrSessionNotFound
	// if the session does not exist.
	TouchSession(ctx context.Context, id string, now time.Time) error
	// DeleteSession removes the session row. Idempotent: returns nil even
	// if the row does not exist (logout is fire-and-forget).
	DeleteSession(ctx context.Context, id string) error
	// DeleteOperatorSessions removes every session belonging to operatorID.
	// Used by § 5 when an operator is disabled or has their role changed.
	DeleteOperatorSessions(ctx context.Context, operatorID string) error
}

// AuditFilter narrows audit-log queries. Empty string fields disable the
// corresponding WHERE clause; nil time pointers disable the time bounds.
// Matches the spec's accepted query parameters (operator, action, target_type,
// target_id, since, until).
type AuditFilter struct {
	OperatorID string
	Action     string
	TargetType string
	TargetID   string
	Since      *time.Time
	Until      *time.Time
}

// AuditStore covers § 6 audit-log persistence + querying. Embeds
// audit.Auditor so the compiler enforces signature parity with the audit
// package's Write contract; ListAuditEntries adds the query side.
//
// ListAuditEntries returns rows ordered by ts DESC, id DESC (the id
// tie-break keeps ordering stable when multiple rows share a
// second-precision ts). Caller clamps offset/limit to D23 bounds
// (default 50, max 500); the store passes them through to SQL.
type AuditStore interface {
	audit.Auditor
	ListAuditEntries(ctx context.Context, filter AuditFilter, offset, limit int) ([]audit.Entry, error)
}

// AccountStore covers the account-lifecycle methods used by the admin API
// (change 2026-05-21-admin-ui-account-lifecycle § 2).
type AccountStore interface {
	CreateAccount(ctx context.Context, in AccountInput, createdByOperatorID string) (*Account, error)
	GetAccount(ctx context.Context, id string) (*Account, error)
	// ListAccounts returns accounts ordered by created_at DESC. statusFilter
	// "" returns all non-deleted accounts; a specific status returns only
	// matches. Pagination follows D23 (default 50, max 500); the caller is
	// responsible for clamping limit.
	ListAccounts(ctx context.Context, statusFilter AccountStatus, offset, limit int) ([]*Account, error)
	UpdateAccount(ctx context.Context, id string, upd AccountUpdate) (*Account, error)
	// DeleteAccountWithCascade atomically revokes every active key owned by
	// the account, sets account.status='deleted', and returns the count of
	// keys revoked. Idempotent: deleting an already-deleted account returns
	// 0 revoked and ErrAccountNotFound (deleted accounts are not visible).
	DeleteAccountWithCascade(ctx context.Context, id string) (revoked int64, err error)
	// CountActiveAccountKeys is used by the DELETE-without-confirm impact
	// preview (account-management spec § "Delete account with safe-lock").
	CountActiveAccountKeys(ctx context.Context, id string) (int64, error)
	// ListAccountKeys returns keys (active + revoked) owned by id ordered by
	// created_at DESC. Pagination follows D23; caller clamps offset/limit.
	ListAccountKeys(ctx context.Context, id string, offset, limit int) ([]*Key, error)
	// CreateKeyForAccount provisions a key owned by an account; the section-3
	// rework of POST /api/v1/keys will route through this once handlers carry
	// account_id end-to-end.
	CreateKeyForAccount(ctx context.Context, accountID, component, label string, expiresAt *time.Time) (*Key, error)
}
