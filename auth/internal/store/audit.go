package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/no42-org/packyard-auth/internal/audit"
)

// SetAuditLogger installs the logger that Write uses when persistence
// fails. Per-store rather than package-global so test parallelism and any
// future multi-instance scenario don't share state. Safe to call before the
// first Write; reads inside Write are protected by the same mutex.
func (s *SQLiteStore) SetAuditLogger(l *slog.Logger) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if l != nil {
		s.auditLogger = l
	}
}

func (s *SQLiteStore) currentAuditLogger() *slog.Logger {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if s.auditLogger == nil {
		return slog.Default()
	}
	return s.auditLogger
}

// Write inserts an audit row. Per the audit.Auditor contract this is
// fire-and-forget: errors do not propagate to the handler because failing
// the operator action because of an audit-write failure is worse than
// missing the audit row. Failures are logged loudly so they show up in
// monitoring; § 10 deployment ops can also probe `audit_log` row growth
// to detect a wedged writer.
//
// Empty (or whitespace-only) Action is rejected up front — `audit_log.action`
// is NOT NULL but does not enforce non-blank, so a whitespace value would
// silently persist and break action-filtered queries. Dropping the row here
// surfaces the bug via the failure log.
func (s *SQLiteStore) Write(ctx context.Context, e audit.Entry) {
	e.Action = strings.TrimSpace(e.Action)
	if e.Action == "" {
		s.logAuditFailure("empty action (caller bug)", e, errAuditEmptyAction)
		return
	}

	detailsJSON := []byte("{}")
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			s.logAuditFailure("marshal audit details", e, err)
			// Surface the loss via a sentinel field so the forensic
			// timeline distinguishes "details lost in marshal" from
			// "no details supplied".
			sentinel, _ := json.Marshal(map[string]any{
				"_marshal_error": err.Error(),
			})
			detailsJSON = sentinel
		} else {
			detailsJSON = b
		}
	}
	// Hard cap on the marshalled JSON to bound audit_log row size against
	// caller bugs that forget to truncate operator-controllable values
	// (label, org_name, paths). Without this cap, a single forgotten
	// TruncateAuditField call site can grow audit rows to 1 MiB+. The
	// 4 KiB cap is well above legitimate detail payloads (~hundreds of
	// bytes for the call sites in this PR) and replaces the body with a
	// sentinel so the forensic timeline still records the event.
	if len(detailsJSON) > auditDetailsMaxBytes {
		sentinel, _ := json.Marshal(map[string]any{
			"_truncated": true,
			"size":       len(detailsJSON),
		})
		detailsJSON = sentinel
	}

	// Use the row's ts default (strftime now) — letting SQLite stamp the
	// timestamp keeps it monotonic with other rows even if the Go clock
	// disagrees with the DB clock by sub-seconds.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (operator_id, action, target_type, target_id, details, ip, user_agent)
		 VALUES (NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''))`,
		e.OperatorID, e.Action, e.TargetType, e.TargetID, string(detailsJSON), e.IP, e.UserAgent,
	)
	if err != nil {
		s.logAuditFailure("insert audit row", e, err)
	}
}

// errAuditEmptyAction signals a caller bug to logAuditFailure.
var errAuditEmptyAction = fmt.Errorf("audit entry has empty Action")

// auditDetailsMaxBytes bounds the size of the marshalled JSON written to
// audit_log.details. Forgotten-truncate call sites would otherwise let an
// attacker-controllable string field grow the row arbitrarily.
const auditDetailsMaxBytes = 4 * 1024

// WarnEmptyOperator implements audit.EmptyOperatorWarner so the empty-
// operator-id warning fires in production (not just for the NoopAuditor).
// Uses the per-store logger so the warning is scoped correctly when
// multiple SQLiteStore instances coexist (tests, future multi-tenant).
func (s *SQLiteStore) WarnEmptyOperator(e audit.Entry) {
	s.currentAuditLogger().Warn("audit entry has empty operator_id",
		slog.String("action", e.Action),
		slog.String("target_type", e.TargetType),
		slog.String("target_id", e.TargetID),
	)
}

func (s *SQLiteStore) logAuditFailure(stage string, e audit.Entry, err error) {
	s.currentAuditLogger().Error("audit log write failed",
		slog.String("stage", stage),
		slog.String("action", e.Action),
		slog.String("operator_id", e.OperatorID),
		slog.String("target_type", e.TargetType),
		slog.String("target_id", e.TargetID),
		slog.String("ip", e.IP),
		slog.String("error", err.Error()),
	)
}

// ListAuditEntries returns rows matching filter, ordered by ts DESC, paged by
// offset/limit. The caller (handler) is responsible for clamping the limit
// to D23 bounds and for fetching `limit+1` to detect more pages.
func (s *SQLiteStore) ListAuditEntries(ctx context.Context, filter AuditFilter, offset, limit int) ([]audit.Entry, error) {
	const cols = `id, ts, operator_id, action, target_type, target_id, details, ip, user_agent`
	query := `SELECT ` + cols + ` FROM audit_log`
	args := make([]any, 0, 8)
	clauses := make([]string, 0, 6)

	if filter.OperatorID != "" {
		clauses = append(clauses, `operator_id = ?`)
		args = append(args, filter.OperatorID)
	}
	if filter.Action != "" {
		clauses = append(clauses, `action = ?`)
		args = append(args, filter.Action)
	}
	if filter.TargetType != "" {
		clauses = append(clauses, `target_type = ?`)
		args = append(args, filter.TargetType)
	}
	if filter.TargetID != "" {
		clauses = append(clauses, `target_id = ?`)
		args = append(args, filter.TargetID)
	}
	if filter.Since != nil {
		clauses = append(clauses, `ts >= ?`)
		args = append(args, filter.Since.UTC().Format(time.RFC3339))
	}
	if filter.Until != nil {
		clauses = append(clauses, `ts < ?`)
		args = append(args, filter.Until.UTC().Format(time.RFC3339))
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	// Tie-break by id DESC so rows with the same ts (RFC3339 second precision)
	// are ordered stably; the autoincrement id is monotonic with insert order.
	query += ` ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()

	out := make([]audit.Entry, 0)
	for rows.Next() {
		e, err := scanAuditRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit rows iteration: %w", err)
	}
	return out, nil
}

func scanAuditRow(s scanner) (audit.Entry, error) {
	var (
		e          audit.Entry
		tsStr      string
		operatorID sql.NullString
		targetType sql.NullString
		targetID   sql.NullString
		detailsStr string
		ip         sql.NullString
		userAgent  sql.NullString
	)
	if err := s.Scan(&e.ID, &tsStr, &operatorID, &e.Action, &targetType, &targetID, &detailsStr, &ip, &userAgent); err != nil {
		return audit.Entry{}, err
	}
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		return audit.Entry{}, fmt.Errorf("parse audit ts: %w", err)
	}
	e.Ts = ts
	if operatorID.Valid {
		e.OperatorID = operatorID.String
	}
	if targetType.Valid {
		e.TargetType = targetType.String
	}
	if targetID.Valid {
		e.TargetID = targetID.String
	}
	if ip.Valid {
		e.IP = ip.String
	}
	if userAgent.Valid {
		e.UserAgent = userAgent.String
	}
	if detailsStr != "" && detailsStr != "{}" {
		var details map[string]any
		if err := json.Unmarshal([]byte(detailsStr), &details); err != nil {
			return audit.Entry{}, fmt.Errorf("unmarshal details: %w", err)
		}
		e.Details = details
	}
	return e, nil
}
