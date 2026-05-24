package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/store"
)

// opsAuditRecorder captures audit entries emitted by the operators handler.
type opsAuditRecorder struct{ entries []audit.Entry }

func (o *opsAuditRecorder) Write(_ context.Context, e audit.Entry) {
	o.entries = append(o.entries, e)
}

func newOperatorsHandler(t *testing.T) (*OperatorsHandler, *store.SQLiteStore, *opsAuditRecorder) {
	t.Helper()
	s := newIntegrationStore(t)
	rec := &opsAuditRecorder{}
	return NewOperatorsHandler(s, s, rec, slog.Default()), s, rec
}

// opReq wraps a request with an injected admin operator (default actor).
func opReq(method, target string, body []byte) *http.Request {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	var req *http.Request
	if rdr != nil {
		req = httptest.NewRequest(method, target, rdr)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	return req.WithContext(auth.WithOperator(req.Context(),
		auth.Operator{ID: "actor-admin", Role: auth.RoleAdmin}))
}

func opReqAs(method, target string, body []byte, op auth.Operator) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	return req.WithContext(auth.WithOperator(req.Context(), op))
}

func chiOpReq(req *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// ─── List ───────────────────────────────────────────────────────────────────

func TestOperators_List_AdminAllowed(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	_, _ = s.AllowlistOperator(context.Background(), "a@x.com", store.OperatorRoleAdmin, "bootstrap")
	_, _ = s.AllowlistOperator(context.Background(), "b@x.com", store.OperatorRoleReadonly, "bootstrap")

	req := opReq(http.MethodGet, "/api/v1/operators", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var ops []store.Operator
	json.NewDecoder(w.Body).Decode(&ops)
	if len(ops) != 2 {
		t.Errorf("want 2 operators, got %d", len(ops))
	}
}

func TestOperators_List_ReadonlyDenied(t *testing.T) {
	h, _, _ := newOperatorsHandler(t)
	req := opReqAs(http.MethodGet, "/api/v1/operators", nil,
		auth.Operator{ID: "ro", Role: auth.RoleReadonly})
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("readonly: want 403, got %d", w.Code)
	}
}

func TestOperators_List_NoOperatorRejected(t *testing.T) {
	h, _, _ := newOperatorsHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/operators", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no operator: want 401, got %d", w.Code)
	}
}

// ─── Create (Allowlist) ─────────────────────────────────────────────────────

func TestOperators_Create_Success(t *testing.T) {
	h, _, rec := newOperatorsHandler(t)
	w := httptest.NewRecorder()
	h.Create(w, opReq(http.MethodPost, "/api/v1/operators",
		[]byte(`{"email":"New@Example.COM","role":"readonly"}`)))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var op store.Operator
	json.NewDecoder(w.Body).Decode(&op)
	if op.Email != "new@example.com" {
		t.Errorf("email should be canonicalised, got %q", op.Email)
	}
	if op.Role != store.OperatorRoleReadonly {
		t.Errorf("role: want readonly, got %q", op.Role)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "operator.add" {
		t.Errorf("want one operator.add audit row, got %+v", rec.entries)
	}
}

func TestOperators_Create_DefaultRoleAdmin(t *testing.T) {
	h, _, _ := newOperatorsHandler(t)
	w := httptest.NewRecorder()
	h.Create(w, opReq(http.MethodPost, "/api/v1/operators", []byte(`{"email":"x@y.com"}`)))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", w.Code)
	}
	var op store.Operator
	json.NewDecoder(w.Body).Decode(&op)
	if op.Role != store.OperatorRoleAdmin {
		t.Errorf("default role should be admin, got %q", op.Role)
	}
}

func TestOperators_Create_DuplicateEmailConflict(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	_, _ = s.AllowlistOperator(context.Background(), "dup@x.com", store.OperatorRoleAdmin, "bootstrap")
	w := httptest.NewRecorder()
	h.Create(w, opReq(http.MethodPost, "/api/v1/operators", []byte(`{"email":"DUP@x.com"}`)))
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate: want 409, got %d", w.Code)
	}
}

func TestOperators_Create_InvalidRole(t *testing.T) {
	h, _, _ := newOperatorsHandler(t)
	w := httptest.NewRecorder()
	h.Create(w, opReq(http.MethodPost, "/api/v1/operators",
		[]byte(`{"email":"x@y.com","role":"superuser"}`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid role: want 400, got %d", w.Code)
	}
}

func TestOperators_Create_ReadonlyDenied(t *testing.T) {
	h, _, _ := newOperatorsHandler(t)
	req := opReqAs(http.MethodPost, "/api/v1/operators",
		[]byte(`{"email":"x@y.com"}`),
		auth.Operator{ID: "ro", Role: auth.RoleReadonly})
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("readonly: want 403, got %d", w.Code)
	}
}

// ─── Update (role + status) ─────────────────────────────────────────────────

func TestOperators_Update_ChangeRoleEmitsAudit(t *testing.T) {
	h, s, rec := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleAdmin, "bootstrap")
	// Add another admin so the self-lockout guard isn't triggered (the
	// actor is admin "actor-admin"; that operator isn't in the DB, but
	// the target itself is admin too — so demoting the target leaves the
	// in-DB count at 0 unless we add another).
	_, _ = s.AllowlistOperator(context.Background(), "other@x.com", store.OperatorRoleAdmin, "bootstrap")

	req := chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"role":"readonly"}`)), target.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "operator.role_change" {
		t.Errorf("want one operator.role_change audit row, got %+v", rec.entries)
	}
	// Detail captures from/to for forensic reconstruction.
	d := rec.entries[0].Details
	if d["from"] != "admin" || d["to"] != "readonly" {
		t.Errorf("role_change details: want from=admin to=readonly, got %v", d)
	}
}

func TestOperators_Update_DisableEmitsAuditAndForcesLogout(t *testing.T) {
	h, s, rec := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleAdmin, "bootstrap")
	// Active session for the target — capture the id so we can verify it
	// gets force-deleted on disable.
	sess, _ := s.CreateSession(context.Background(), target.ID, "1.1.1.1", "test")
	// Extra admin so disable is allowed (self-lockout guard).
	_, _ = s.AllowlistOperator(context.Background(), "other@x.com", store.OperatorRoleAdmin, "bootstrap")

	req := chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"status":"disabled"}`)), target.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "operator.disable" {
		t.Errorf("want one operator.disable audit row, got %+v", rec.entries)
	}
	// Force-logout: the session we created above should be gone.
	if _, err := s.GetSession(context.Background(), sess.ID); err == nil {
		t.Errorf("disable should force-logout target sessions; session still present")
	}
}

func TestOperators_Update_SelfLockoutGuard(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	// Make actor the only admin in the DB.
	actor, _ := s.AllowlistOperator(context.Background(), "only@admin.com", store.OperatorRoleAdmin, "bootstrap")

	// Try to demote self when no other active admin exists.
	req := chiOpReq(opReqAs(http.MethodPatch, "/api/v1/operators/"+actor.ID,
		[]byte(`{"role":"readonly"}`),
		auth.Operator{ID: actor.ID, Role: auth.RoleAdmin}),
		actor.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("self-demote with no other admin: want 403, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "OPERATOR_SELF_LOCKOUT" {
		t.Errorf("want OPERATOR_SELF_LOCKOUT, got %q", ae.Code)
	}
}

func TestOperators_Update_SelfLockoutAllowedWithOtherAdmin(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	actor, _ := s.AllowlistOperator(context.Background(), "actor@x.com", store.OperatorRoleAdmin, "bootstrap")
	_, _ = s.AllowlistOperator(context.Background(), "other@x.com", store.OperatorRoleAdmin, "bootstrap")

	req := chiOpReq(opReqAs(http.MethodPatch, "/api/v1/operators/"+actor.ID,
		[]byte(`{"role":"readonly"}`),
		auth.Operator{ID: actor.ID, Role: auth.RoleAdmin}),
		actor.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("self-demote with another admin: want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOperators_Update_NotFound(t *testing.T) {
	h, _, _ := newOperatorsHandler(t)
	req := chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/missing",
		[]byte(`{"role":"readonly"}`)), "missing")
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestOperators_Update_ReadonlyDenied(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleAdmin, "bootstrap")
	req := chiOpReq(opReqAs(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"role":"readonly"}`),
		auth.Operator{ID: "ro", Role: auth.RoleReadonly}),
		target.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("readonly: want 403, got %d", w.Code)
	}
}

// TestOperators_Update_CrossAdminLockout exercises the global guard: admin A
// demoting the LAST OTHER admin B must be refused. Under the old actor-only
// guard this passed silently and left zero admins after A's session lapsed.
func TestOperators_Update_CrossAdminLockout(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	// Only admin in the DB is the target; the actor isn't in the DB (synthetic
	// session context). Demoting target leaves zero DB-admins.
	target, _ := s.AllowlistOperator(context.Background(), "lastadmin@x.com", store.OperatorRoleAdmin, "bootstrap")

	req := chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"role":"readonly"}`)), target.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-admin demote of last admin: want 403, got %d: %s", w.Code, w.Body.String())
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "OPERATOR_SELF_LOCKOUT" {
		t.Errorf("want OPERATOR_SELF_LOCKOUT, got %q", ae.Code)
	}
}

// TestOperators_Update_CrossAdminDisableLockout mirrors the cross-admin guard
// for status='disabled' (the more dangerous lockout path — no role change,
// just status flip on the only active admin).
func TestOperators_Update_CrossAdminDisableLockout(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "lastadmin@x.com", store.OperatorRoleAdmin, "bootstrap")

	req := chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"status":"disabled"}`)), target.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-admin disable of last admin: want 403, got %d: %s", w.Code, w.Body.String())
	}
}

// TestOperators_Update_EmptyBody rejects no-op PATCHes with 400 so client
// bugs surface immediately rather than masquerading as quiet successes.
func TestOperators_Update_EmptyBody(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleAdmin, "bootstrap")
	req := chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID, []byte(`{}`)), target.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty body: want 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestOperators_Update_InvalidStatus mirrors the invalid-role check.
func TestOperators_Update_InvalidStatus(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleAdmin, "bootstrap")
	req := chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"status":"pending"}`)), target.ID)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid status: want 400, got %d", w.Code)
	}
}

// TestOperators_Update_EnableRoundTripEmitsAudit verifies the disable→enable
// round-trip emits both `operator.disable` and `operator.enable` audit rows
// with from/to details (additive vocabulary recorded in the spec).
func TestOperators_Update_EnableRoundTripEmitsAudit(t *testing.T) {
	h, s, rec := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleAdmin, "bootstrap")
	// Extra admin so the disable passes the lockout guard.
	_, _ = s.AllowlistOperator(context.Background(), "other@x.com", store.OperatorRoleAdmin, "bootstrap")

	// Disable.
	wDisable := httptest.NewRecorder()
	h.Update(wDisable, chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"status":"disabled"}`)), target.ID))
	if wDisable.Code != http.StatusOK {
		t.Fatalf("disable: want 200, got %d", wDisable.Code)
	}

	// Re-enable.
	wEnable := httptest.NewRecorder()
	h.Update(wEnable, chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"status":"active"}`)), target.ID))
	if wEnable.Code != http.StatusOK {
		t.Fatalf("enable: want 200, got %d: %s", wEnable.Code, wEnable.Body.String())
	}

	if len(rec.entries) != 2 {
		t.Fatalf("want 2 audit rows (disable + enable), got %d: %+v", len(rec.entries), rec.entries)
	}
	if rec.entries[0].Action != "operator.disable" {
		t.Errorf("first audit row action: want operator.disable, got %q", rec.entries[0].Action)
	}
	if rec.entries[1].Action != "operator.enable" {
		t.Errorf("second audit row action: want operator.enable, got %q", rec.entries[1].Action)
	}
	if d := rec.entries[1].Details; d["from"] != "disabled" || d["to"] != "active" {
		t.Errorf("enable details: want from=disabled to=active, got %v", d)
	}
}

// TestOperators_Update_RoleChangeForcesLogout covers the demote-side force-
// logout that previously only had the status-disable test.
func TestOperators_Update_RoleChangeForcesLogout(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleAdmin, "bootstrap")
	sess, _ := s.CreateSession(context.Background(), target.ID, "1.1.1.1", "test")
	_, _ = s.AllowlistOperator(context.Background(), "other@x.com", store.OperatorRoleAdmin, "bootstrap")

	w := httptest.NewRecorder()
	h.Update(w, chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"role":"readonly"}`)), target.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("demote: want 200, got %d", w.Code)
	}
	if _, err := s.GetSession(context.Background(), sess.ID); err == nil {
		t.Errorf("demote should force-logout target sessions; session still present")
	}
}

// TestOperators_Update_PromoteSkipsForceLogout confirms readonly→admin
// promotion does NOT delete sessions (the operator's existing session is
// still valid; their next request will see the elevated role via the
// per-request operator re-read in session middleware).
func TestOperators_Update_PromoteSkipsForceLogout(t *testing.T) {
	h, s, _ := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleReadonly, "bootstrap")
	sess, _ := s.CreateSession(context.Background(), target.ID, "1.1.1.1", "test")

	w := httptest.NewRecorder()
	h.Update(w, chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"role":"admin"}`)), target.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("promote: want 200, got %d", w.Code)
	}
	if _, err := s.GetSession(context.Background(), sess.ID); err != nil {
		t.Errorf("promote should keep session; got err %v", err)
	}
}

// TestOperators_Update_AtomicRoleAndStatus exercises a combined role+status
// PATCH. Both fields commit in a single UPDATE inside one transaction; the
// handler emits one audit row per mutation type.
func TestOperators_Update_AtomicRoleAndStatus(t *testing.T) {
	h, s, rec := newOperatorsHandler(t)
	target, _ := s.AllowlistOperator(context.Background(), "t@x.com", store.OperatorRoleAdmin, "bootstrap")
	_, _ = s.AllowlistOperator(context.Background(), "other@x.com", store.OperatorRoleAdmin, "bootstrap")

	w := httptest.NewRecorder()
	h.Update(w, chiOpReq(opReq(http.MethodPatch, "/api/v1/operators/"+target.ID,
		[]byte(`{"role":"readonly","status":"disabled"}`)), target.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("combined PATCH: want 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, _ := s.GetOperator(context.Background(), target.ID)
	if updated.Role != store.OperatorRoleReadonly || updated.Status != store.OperatorStatusDisabled {
		t.Errorf("after combined PATCH: want readonly+disabled, got %s+%s", updated.Role, updated.Status)
	}
	if len(rec.entries) != 2 {
		t.Errorf("want 2 audit rows (role_change + disable), got %d: %+v", len(rec.entries), rec.entries)
	}
}
