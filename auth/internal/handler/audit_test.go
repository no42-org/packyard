/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/store"
)

func newAuditHandler(t *testing.T) (*AuditHandler, *store.SQLiteStore) {
	t.Helper()
	s := newIntegrationStore(t)
	return NewAuditHandler(s, slog.Default()), s
}

// auditReq builds a GET request to /api/v1/audit with an injected operator
// in the context — the handler now enforces operator presence as defense in
// depth (§ 6 review patch).
func auditReq(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	return req.WithContext(auth.WithOperator(req.Context(),
		auth.Operator{ID: "test-op", Role: auth.RoleReadonly}))
}

func seedAuditEntries(s *store.SQLiteStore, entries ...audit.Entry) {
	ctx := context.Background()
	for _, e := range entries {
		s.Write(ctx, e)
	}
}

func TestAudit_List_HappyPath(t *testing.T) {
	h, s := newAuditHandler(t)
	seedAuditEntries(s,
		audit.Entry{OperatorID: "op-1", Action: "account.create", TargetType: "account", TargetID: "a1"},
		audit.Entry{OperatorID: "op-1", Action: "key.issue", TargetType: "key", TargetID: "k1"},
	)

	req := auditReq("/api/v1/audit")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var rows []auditResponseRow
	json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 2 {
		t.Errorf("want 2 rows, got %d", len(rows))
	}
}

func TestAudit_List_FilterByOperator(t *testing.T) {
	h, s := newAuditHandler(t)
	seedAuditEntries(s,
		audit.Entry{OperatorID: "op-a", Action: "x"},
		audit.Entry{OperatorID: "op-b", Action: "x"},
	)

	req := auditReq("/api/v1/audit?operator=op-a")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var rows []auditResponseRow
	json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 1 || rows[0].OperatorID != "op-a" {
		t.Errorf("operator filter: want one op-a row, got %v", rows)
	}
}

func TestAudit_List_FilterByActionAndTarget(t *testing.T) {
	h, s := newAuditHandler(t)
	seedAuditEntries(s,
		audit.Entry{Action: "key.issue", TargetType: "key", TargetID: "k1"},
		audit.Entry{Action: "key.revoke", TargetType: "key", TargetID: "k1"},
		audit.Entry{Action: "key.issue", TargetType: "key", TargetID: "k2"},
	)

	req := auditReq("/api/v1/audit?action=key.issue&target_type=key&target_id=k2")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var rows []auditResponseRow
	json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 1 || rows[0].TargetID != "k2" {
		t.Errorf("combined filter: want one k2/issue row, got %v", rows)
	}
}

func TestAudit_List_InvalidSince(t *testing.T) {
	h, _ := newAuditHandler(t)
	req := auditReq("/api/v1/audit?since=not-a-date")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid since: want 400, got %d", w.Code)
	}
}

func TestAudit_List_LimitTooLarge(t *testing.T) {
	h, _ := newAuditHandler(t)
	req := auditReq("/api/v1/audit?limit=501")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "LIMIT_TOO_LARGE" {
		t.Errorf("want LIMIT_TOO_LARGE, got %q", ae.Code)
	}
}

func TestAudit_List_PaginationLinkNext(t *testing.T) {
	h, s := newAuditHandler(t)
	for i := 0; i < 5; i++ {
		s.Write(context.Background(), audit.Entry{Action: "x"})
	}
	req := auditReq("/api/v1/audit?offset=0&limit=2")
	w := httptest.NewRecorder()
	h.List(w, req)
	link := w.Header().Get("Link")
	if !bytesContains(link, `offset=2`) {
		t.Errorf("expected Link rel=next pointing to offset=2, got %q", link)
	}
}

// TestAudit_List_NoOperatorRejected verifies the defensive in-handler
// operator-presence check — a request that somehow bypassed session
// middleware must still 401, not leak audit rows unauthenticated.
func TestAudit_List_NoOperatorRejected(t *testing.T) {
	h, _ := newAuditHandler(t)
	// Raw request without WithOperator.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no operator: want 401, got %d", w.Code)
	}
}

// TestAudit_List_ReturnsIDAndTs verifies that responses carry the spec-
// missing fields (closes the CRITICAL response-shape gap from review).
func TestAudit_List_ReturnsIDAndTs(t *testing.T) {
	h, s := newAuditHandler(t)
	seedAuditEntries(s, audit.Entry{Action: "x", OperatorID: "op-1"})

	req := auditReq("/api/v1/audit")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var rows []auditResponseRow
	json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].ID == 0 {
		t.Errorf("id should be non-zero (autoincrement)")
	}
	if rows[0].Ts.IsZero() {
		t.Errorf("ts should be populated from the DB default")
	}
}

// TestAudit_List_SinceAfterUntilRejected covers the new mistyped-window
// validation.
func TestAudit_List_SinceAfterUntilRejected(t *testing.T) {
	h, _ := newAuditHandler(t)
	req := auditReq("/api/v1/audit?since=2026-12-31T00:00:00Z&until=2026-01-01T00:00:00Z")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

// TestAudit_List_NegativeOffsetRejected — parsePagination now rejects
// negative inputs instead of silently coercing to 0.
func TestAudit_List_NegativeOffsetRejected(t *testing.T) {
	h, _ := newAuditHandler(t)
	req := auditReq("/api/v1/audit?offset=-1")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("negative offset: want 400, got %d", w.Code)
	}
}

// TestAudit_List_ZeroLimitRejected — `?limit=0` was previously silently
// coerced to defaultPageLimit (50); the parsePagination patch now returns
// 400 INVALID_REQUEST. Behaviour change worth a regression guard.
func TestAudit_List_ZeroLimitRejected(t *testing.T) {
	h, _ := newAuditHandler(t)
	req := auditReq("/api/v1/audit?limit=0")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("limit=0: want 400, got %d", w.Code)
	}
}

// TestAudit_List_NonIntegerLimitRejected — `?limit=abc` was previously
// silently coerced; now 400.
func TestAudit_List_NonIntegerLimitRejected(t *testing.T) {
	h, _ := newAuditHandler(t)
	req := auditReq("/api/v1/audit?limit=abc")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("limit=abc: want 400, got %d", w.Code)
	}
}

// TestAudit_List_InvalidUntilRejected — symmetric to the existing
// invalid-since test.
func TestAudit_List_InvalidUntilRejected(t *testing.T) {
	h, _ := newAuditHandler(t)
	req := auditReq("/api/v1/audit?until=garbage")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid until: want 400, got %d", w.Code)
	}
}

// TestAudit_List_HasMoreBoundary verifies the limit+1 fetch correctly
// distinguishes "exactly limit rows" (hasMore=false) from "more than limit"
// (hasMore=true).
func TestAudit_List_HasMoreBoundary(t *testing.T) {
	h, s := newAuditHandler(t)
	// Seed exactly 3 rows; limit=3 → expect 3 rows, no rel="next".
	for i := 0; i < 3; i++ {
		s.Write(context.Background(), audit.Entry{Action: "x"})
	}
	req := auditReq("/api/v1/audit?limit=3")
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	link := w.Header().Get("Link")
	if bytesContains(link, `rel="next"`) {
		t.Errorf("limit==count should NOT emit rel=next; got %q", link)
	}

	// One more row → limit=3 → expect 3 rows + rel="next".
	s.Write(context.Background(), audit.Entry{Action: "y"})
	req = auditReq("/api/v1/audit?limit=3")
	w = httptest.NewRecorder()
	h.List(w, req)
	link = w.Header().Get("Link")
	if !bytesContains(link, `rel="next"`) {
		t.Errorf("4 rows with limit=3 should emit rel=next; got %q", link)
	}
}

// TestAudit_RoundTrip_ViaAccountsHandler — proves that mutating handlers
// now persist audit rows reachable via GET /api/v1/audit. Closes § 6.2.
func TestAudit_RoundTrip_ViaAccountsHandler(t *testing.T) {
	s := newIntegrationStore(t)
	if _, err := s.CreateComponent(context.Background(), &store.Component{Name: "core", Visibility: "private"}); err != nil {
		t.Fatalf("seed component: %v", err)
	}
	accountsH := NewAccountsHandler(s, s, s, slog.Default(), map[string]string{"core": "private"})
	auditH := NewAuditHandler(s, slog.Default())

	// Create an account through the handler (writes account.create audit).
	w := postAccount(accountsH, `{"email":"e2e@example.com"}`, "admin")

	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body.String())
	}

	// Query via the audit handler.
	req := auditReq("/api/v1/audit?action=account.create")
	rec := httptest.NewRecorder()
	auditH.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list: want 200, got %d", rec.Code)
	}
	var rows []auditResponseRow
	json.NewDecoder(rec.Body).Decode(&rows)
	if len(rows) != 1 {
		t.Fatalf("want exactly one persisted audit row, got %d", len(rows))
	}
	if rows[0].Action != "account.create" {
		t.Errorf("action: want account.create, got %q", rows[0].Action)
	}
	if rows[0].OperatorID == "" {
		t.Errorf("operator_id should be auto-filled by audit.WriteFromRequest")
	}
}
