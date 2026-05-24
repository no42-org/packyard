package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/store"
)

// newAccountsHandlerWithStore returns a handler backed by an in-memory SQLite
// store with one provisioned component so issue-key tests have a valid target.
func newAccountsHandlerWithStore(t *testing.T) (*AccountsHandler, *store.SQLiteStore) {
	t.Helper()
	s := newIntegrationStore(t)
	if _, err := s.CreateComponent(context.Background(), &store.Component{
		Name:       "core",
		Visibility: "private",
	}); err != nil {
		t.Fatalf("seed component: %v", err)
	}
	vis := map[string]string{"core": "private"}
	return NewAccountsHandler(s, s, nil, slog.Default(), vis), s
}

// withOperatorRole returns a request whose context carries an operator of the
// requested role, so role-gated handlers behave deterministically in tests.
func withOperatorRole(req *http.Request, role auth.Role) *http.Request {
	return req.WithContext(auth.WithOperator(req.Context(), auth.Operator{
		ID:   "test-op",
		Role: role,
	}))
}

// chiAccountReq wraps the request in a chi RouteContext with the id URL param.
func chiAccountReq(req *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func postAccount(h *AccountsHandler, body string, role auth.Role) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, role)
	w := httptest.NewRecorder()
	h.Create(w, req)
	return w
}

// ─── Account creation ───────────────────────────────────────────────────────

func TestAccounts_Create_Success(t *testing.T) {
	h, _ := newAccountsHandlerWithStore(t)
	w := postAccount(h, `{"email":"Customer@Example.COM","org_name":"Acme"}`, auth.RoleAdmin)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var a store.Account
	if err := json.NewDecoder(w.Body).Decode(&a); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if a.Email != "customer@example.com" {
		t.Errorf("expected canonicalised email, got %q", a.Email)
	}
	if a.OrgName != "Acme" {
		t.Errorf("expected org_name 'Acme', got %q", a.OrgName)
	}
	if a.Status != store.AccountStatusActive {
		t.Errorf("expected status active, got %q", a.Status)
	}
	if a.CreatedByOperatorID != "test-op" {
		t.Errorf("expected created_by 'test-op', got %q", a.CreatedByOperatorID)
	}
}

func TestAccounts_Create_DuplicateEmail(t *testing.T) {
	h, _ := newAccountsHandlerWithStore(t)
	postAccount(h, `{"email":"a@x.com"}`, auth.RoleAdmin)
	w := postAccount(h, `{"email":"A@X.COM"}`, auth.RoleAdmin)

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "ACCOUNT_EMAIL_EXISTS" {
		t.Errorf("want ACCOUNT_EMAIL_EXISTS, got %q", ae.Code)
	}
}

func TestAccounts_Create_ReadonlyDenied(t *testing.T) {
	h, _ := newAccountsHandlerWithStore(t)
	w := postAccount(h, `{"email":"x@y.com"}`, auth.RoleReadonly)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "ROLE_DENIED" {
		t.Errorf("want ROLE_DENIED, got %q", ae.Code)
	}
}

func TestAccounts_Create_EmptyEmailRejected(t *testing.T) {
	h, _ := newAccountsHandlerWithStore(t)
	w := postAccount(h, `{"email":""}`, auth.RoleAdmin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

// ─── List + pagination ──────────────────────────────────────────────────────

func TestAccounts_List_ExcludesDeleted(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a1, _ := s.CreateAccount(ctx, store.AccountInput{Email: "del@x.com"}, "op")
	_, _ = s.CreateAccount(ctx, store.AccountInput{Email: "live@x.com"}, "op")
	_, _ = s.DeleteAccountWithCascade(ctx, a1.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var accounts []store.Account
	json.NewDecoder(w.Body).Decode(&accounts)
	// legacy (seed) + live, but not del.
	if len(accounts) != 2 {
		t.Errorf("want 2 (legacy+live), got %d", len(accounts))
	}
	for _, a := range accounts {
		if a.ID == a1.ID {
			t.Errorf("deleted account leaked in default list: %v", a)
		}
	}
}

func TestAccounts_List_LimitTooLarge(t *testing.T) {
	h, _ := newAccountsHandlerWithStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?limit=501", nil)
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

func TestAccounts_List_LinkHeaderNext(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = s.CreateAccount(ctx, store.AccountInput{Email: fmt.Sprintf("a%d@x.com", i)}, "op")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?offset=0&limit=3", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	link := w.Header().Get("Link")
	// url.Values.Encode sorts keys alphabetically, so "limit" precedes "offset".
	if !bytesContains(link, `limit=3&offset=3>; rel="next"`) {
		t.Errorf("expected Link rel=next pointing to offset=3, got %q", link)
	}
}

// ─── Get / not-found / deleted ──────────────────────────────────────────────

func TestAccounts_Get_NotFound(t *testing.T) {
	h, _ := newAccountsHandlerWithStore(t)
	req := chiAccountReq(httptest.NewRequest(http.MethodGet, "/api/v1/accounts/unknown", nil), "unknown")
	w := httptest.NewRecorder()
	h.Get(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "ACCOUNT_NOT_FOUND" {
		t.Errorf("want ACCOUNT_NOT_FOUND, got %q", ae.Code)
	}
}

func TestAccounts_Get_DeletedReturns404(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")
	_, _ = s.DeleteAccountWithCascade(ctx, a.ID)

	req := chiAccountReq(httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+a.ID, nil), a.ID)
	w := httptest.NewRecorder()
	h.Get(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 for deleted account, got %d", w.Code)
	}
}

// ─── PATCH update / status transitions ──────────────────────────────────────

func TestAccounts_Update_SuspendDoesNotTouchKeys(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")
	k, _ := s.CreateKeyForAccount(ctx, a.ID, "core", "k", nil)

	body, _ := json.Marshal(map[string]any{"status": "suspended"})
	req := chiAccountReq(httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/"+a.ID, bytes.NewReader(body)), a.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Key must still be active.
	got, err := s.GetByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("GetByID after suspend: %v", err)
	}
	if !got.Active {
		t.Error("key was deactivated during suspend")
	}
}

func TestAccounts_Update_RejectsDeletedTransition(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")

	body, _ := json.Marshal(map[string]any{"status": "deleted"})
	req := chiAccountReq(httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/"+a.ID, bytes.NewReader(body)), a.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for deleted transition, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "INVALID_STATUS_TRANSITION" {
		t.Errorf("want INVALID_STATUS_TRANSITION, got %q", ae.Code)
	}
}

// ─── DELETE / impact preview / cascade ──────────────────────────────────────

func TestAccounts_Delete_RequiresConfirm(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")
	for i := 0; i < 3; i++ {
		_, _ = s.CreateKeyForAccount(ctx, a.ID, "core", "k", nil)
	}

	req := chiAccountReq(httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/"+a.ID, nil), a.ID)
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 without confirm, got %d", w.Code)
	}
	var preview deleteImpactPreview
	json.NewDecoder(w.Body).Decode(&preview)
	if preview.Code != "CONFIRM_REQUIRED" {
		t.Errorf("want CONFIRM_REQUIRED, got %q", preview.Code)
	}
	if preview.Impact.KeysRevoked != 3 {
		t.Errorf("want impact.keys_revoked=3, got %d", preview.Impact.KeysRevoked)
	}
}

func TestAccounts_Delete_WithConfirmCascades(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")
	for i := 0; i < 3; i++ {
		_, _ = s.CreateKeyForAccount(ctx, a.ID, "core", "k", nil)
	}

	url := "/api/v1/accounts/" + a.ID + "?confirm=" + a.ID
	req := chiAccountReq(httptest.NewRequest(http.MethodDelete, url, nil), a.ID)
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var res accountDeleteResult
	json.NewDecoder(w.Body).Decode(&res)
	if res.KeysRevoked != 3 {
		t.Errorf("want keys_revoked=3, got %d", res.KeysRevoked)
	}

	// Verify cascade actually happened.
	keys, _ := s.ListAccountKeys(ctx, a.ID, 0, 50)
	for _, k := range keys {
		if k.Active {
			t.Errorf("key %s still active after cascade-delete", k.ID)
		}
	}
}

func TestAccounts_Delete_ConfirmMismatch(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")

	url := "/api/v1/accounts/" + a.ID + "?confirm=wrong"
	req := chiAccountReq(httptest.NewRequest(http.MethodDelete, url, nil), a.ID)
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 on mismatched confirm, got %d", w.Code)
	}
}

// ─── Account-scoped keys ────────────────────────────────────────────────────

func TestAccounts_ListKeys_IncludesRevoked(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")
	k1, _ := s.CreateKeyForAccount(ctx, a.ID, "core", "active", nil)
	_, _ = s.CreateKeyForAccount(ctx, a.ID, "core", "also-active", nil)
	k3, _ := s.CreateKeyForAccount(ctx, a.ID, "core", "tobe-revoked", nil)
	_ = s.RevokeKey(ctx, k3.ID)

	req := chiAccountReq(httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+a.ID+"/keys", nil), a.ID)
	w := httptest.NewRecorder()
	h.ListKeys(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var keys []keyResponse
	json.NewDecoder(w.Body).Decode(&keys)
	if len(keys) != 3 {
		t.Fatalf("want 3 keys (active+revoked), got %d", len(keys))
	}
	var foundRevoked, foundFirst bool
	for _, k := range keys {
		if k.ID == k1.ID {
			foundFirst = true
		}
		if k.ID == k3.ID && !k.Active {
			foundRevoked = true
		}
	}
	if !foundFirst || !foundRevoked {
		t.Errorf("expected to find both first key and revoked key; got firstFound=%v revokedFound=%v", foundFirst, foundRevoked)
	}
}

func TestAccounts_IssueKey_CannotIssueForDeletedAccount(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")
	_, _ = s.DeleteAccountWithCascade(ctx, a.ID)

	body := `{"component":"core","label":"x"}`
	req := chiAccountReq(httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+a.ID+"/keys", bytes.NewBufferString(body)), a.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.IssueKey(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for deleted account, got %d", w.Code)
	}
}

func TestAccounts_IssueKey_InvalidComponent(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")

	body := `{"component":"nonexistent"}`
	req := chiAccountReq(httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+a.ID+"/keys", bytes.NewBufferString(body)), a.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.IssueKey(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "INVALID_COMPONENT" {
		t.Errorf("want INVALID_COMPONENT, got %q", ae.Code)
	}
}

func TestAccounts_IssueKey_Success(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "x@y.com"}, "op")

	body := `{"component":"core","label":"build-server"}`
	req := chiAccountReq(httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+a.ID+"/keys", bytes.NewBufferString(body)), a.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.IssueKey(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var key keyResponse
	json.NewDecoder(w.Body).Decode(&key)
	if key.AccountID != a.ID {
		t.Errorf("key.account_id mismatch: want %q, got %q", a.ID, key.AccountID)
	}
	if key.Component != "core" {
		t.Errorf("key.component want core, got %q", key.Component)
	}
}

// ─── Forward-auth account-status gate (task 2.5) ────────────────────────────

func TestForwardAuth_SuspendedAccountDeniedReversibly(t *testing.T) {
	s := newIntegrationStore(t)
	ctx := context.Background()

	if _, err := s.CreateComponent(ctx, &store.Component{Name: "core", Visibility: "private"}); err != nil {
		t.Fatalf("seed component: %v", err)
	}
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "sub@x.com"}, "op")
	k, _ := s.CreateKeyForAccount(ctx, a.ID, "core", "k", nil)

	h := NewForwardAuthHandler(s, s, s, slog.Default())

	doReq := func() int {
		req := httptest.NewRequest(http.MethodGet, "/auth", nil)
		req.Header.Set("Authorization", basicAuthHeader(k.ID))
		req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	// Active: allowed.
	if code := doReq(); code != http.StatusOK {
		t.Fatalf("active account: want 200, got %d", code)
	}

	// Suspend.
	suspended := store.AccountStatusSuspended
	if _, err := s.UpdateAccount(ctx, a.ID, store.AccountUpdate{Status: &suspended}); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if code := doReq(); code != http.StatusUnauthorized {
		t.Errorf("suspended account: want 401, got %d", code)
	}
	// Key must NOT have been mutated (D11).
	got, _ := s.GetByID(ctx, k.ID)
	if !got.Active {
		t.Errorf("key.active changed during suspend; D11 violated")
	}

	// Reactivate: subscriber access restored without redeployment.
	active := store.AccountStatusActive
	if _, err := s.UpdateAccount(ctx, a.ID, store.AccountUpdate{Status: &active}); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if code := doReq(); code != http.StatusOK {
		t.Errorf("reactivated account: want 200, got %d", code)
	}
}

func TestForwardAuth_DeletedAccountDenied(t *testing.T) {
	s := newIntegrationStore(t)
	ctx := context.Background()

	if _, err := s.CreateComponent(ctx, &store.Component{Name: "core", Visibility: "private"}); err != nil {
		t.Fatalf("seed component: %v", err)
	}
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "del@x.com"}, "op")
	k, _ := s.CreateKeyForAccount(ctx, a.ID, "core", "k", nil)
	_, _ = s.DeleteAccountWithCascade(ctx, a.ID)

	h := NewForwardAuthHandler(s, s, s, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(k.ID))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Delete cascades revoke first; the key itself is revoked so 401 even
	// before the account gate triggers. Both paths reach the same outcome.
	if w.Code != http.StatusUnauthorized {
		t.Errorf("deleted account: want 401, got %d", w.Code)
	}
}

// bytesContains is a tiny readable alternative to strings.Contains used in a
// couple of the assertions above; keeps the dependency surface tight.
func bytesContains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ─── Tests added from § 2+3 review patches ──────────────────────────────────

// recordingAuditor captures all Write calls so tests can assert that the
// handler invoked the audit layer with the expected entry.
type recordingAuditor struct {
	entries []audit.Entry
}

func (r *recordingAuditor) Write(_ context.Context, e audit.Entry) {
	r.entries = append(r.entries, e)
}

func newAccountsHandlerWithAuditor(t *testing.T) (*AccountsHandler, *store.SQLiteStore, *recordingAuditor) {
	t.Helper()
	s := newIntegrationStore(t)
	if _, err := s.CreateComponent(context.Background(), &store.Component{
		Name:       "core",
		Visibility: "private",
	}); err != nil {
		t.Fatalf("seed component: %v", err)
	}
	rec := &recordingAuditor{}
	return NewAccountsHandler(s, s, rec, slog.Default(), map[string]string{"core": "private"}), s, rec
}

// TestAccounts_Audit_CreateUpdateDeleteIssue verifies the handler writes
// audit entries for every mutation. Regression guard against silently dropping
// the audit calls.
func TestAccounts_Audit_CreateUpdateDeleteIssue(t *testing.T) {
	h, s, rec := newAccountsHandlerWithAuditor(t)
	ctx := context.Background()

	// Create
	w := postAccount(h, `{"email":"auditme@x.com"}`, auth.RoleAdmin)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d", w.Code)
	}
	var created store.Account
	json.NewDecoder(w.Body).Decode(&created)

	// Suspend
	body, _ := json.Marshal(map[string]any{"status": "suspended"})
	req := chiAccountReq(httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/"+created.ID, bytes.NewReader(body)), created.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	wp := httptest.NewRecorder()
	h.Update(wp, req)
	if wp.Code != http.StatusOK {
		t.Fatalf("suspend: want 200, got %d", wp.Code)
	}

	// Reactivate
	body, _ = json.Marshal(map[string]any{"status": "active"})
	req = chiAccountReq(httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/"+created.ID, bytes.NewReader(body)), created.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	wp = httptest.NewRecorder()
	h.Update(wp, req)

	// Issue key
	body, _ = json.Marshal(map[string]any{"component": "core", "label": "k"})
	req = chiAccountReq(httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+created.ID+"/keys", bytes.NewReader(body)), created.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	wk := httptest.NewRecorder()
	h.IssueKey(wk, req)
	if wk.Code != http.StatusCreated {
		t.Fatalf("issue key: want 201, got %d", wk.Code)
	}

	// Delete with confirm
	url := "/api/v1/accounts/" + created.ID + "?confirm=" + created.ID
	req = chiAccountReq(httptest.NewRequest(http.MethodDelete, url, nil), created.ID)
	req = withOperatorRole(req, auth.RoleAdmin)
	wd := httptest.NewRecorder()
	h.Delete(wd, req)
	if wd.Code != http.StatusOK {
		t.Fatalf("delete: want 200, got %d", wd.Code)
	}

	// Assert the sequence of audit actions.
	want := []string{"account.create", "account.suspend", "account.reactivate", "key.issue", "account.delete"}
	if len(rec.entries) != len(want) {
		t.Fatalf("audit entries: want %d, got %d (%v)", len(want), len(rec.entries), rec.entries)
	}
	for i, e := range rec.entries {
		if e.Action != want[i] {
			t.Errorf("audit[%d].Action: want %q, got %q", i, want[i], e.Action)
		}
		if e.OperatorID != "test-op" {
			t.Errorf("audit[%d].OperatorID: want test-op, got %q", i, e.OperatorID)
		}
	}
	_ = ctx
	_ = s
}

// TestAccounts_Delete_DoesNotTouchOtherAccountsKeys verifies cross-account
// isolation: cascade-revoke on account A must not affect account B's keys.
func TestAccounts_Delete_DoesNotTouchOtherAccountsKeys(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()

	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "a@x.com"}, "op")
	b, _ := s.CreateAccount(ctx, store.AccountInput{Email: "b@x.com"}, "op")
	for i := 0; i < 2; i++ {
		_, _ = s.CreateKeyForAccount(ctx, a.ID, "core", "ak", nil)
		_, _ = s.CreateKeyForAccount(ctx, b.ID, "core", "bk", nil)
	}

	url := "/api/v1/accounts/" + a.ID + "?confirm=" + a.ID
	req := chiAccountReq(httptest.NewRequest(http.MethodDelete, url, nil), a.ID)
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete a: want 200, got %d", w.Code)
	}

	bKeys, _ := s.ListAccountKeys(ctx, b.ID, 0, 50)
	if len(bKeys) != 2 {
		t.Fatalf("b should still own 2 keys, got %d", len(bKeys))
	}
	for _, k := range bKeys {
		if !k.Active {
			t.Errorf("b's key %s was revoked during a's delete (cross-account leak)", k.ID)
		}
	}
}

// TestForwardAuth_DeletedAccountGate verifies the deleted-account branch in
// forward_auth.go independently of key revocation. The real store cascades
// revoke on account delete, which would mask this branch — so we use a stub
// AccountStore that returns ErrAccountNotFound for an account_id while the
// underlying key remains active in the key store.
func TestForwardAuth_DeletedAccountGate(t *testing.T) {
	s := newIntegrationStore(t)
	ctx := context.Background()

	if _, err := s.CreateComponent(ctx, &store.Component{Name: "core", Visibility: "private"}); err != nil {
		t.Fatalf("seed component: %v", err)
	}
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "gate@x.com"}, "op")
	k, _ := s.CreateKeyForAccount(ctx, a.ID, "core", "k", nil)

	// Stub AccountStore that hides the account (simulating soft-delete from
	// the API surface) while the key store still serves the active key.
	deletedStub := &stubAccountForGate{hiddenID: a.ID, base: s}

	h := NewForwardAuthHandler(s, s, deletedStub, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(k.ID))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("deleted-account gate: want 401, got %d", w.Code)
	}

	// And the key row must still be active (not mutated by the gate).
	got, _ := s.GetByID(ctx, k.ID)
	if !got.Active {
		t.Error("key.active was mutated by the forward-auth gate; D11 violated")
	}
}

// stubAccountForGate simulates a deleted account by returning
// ErrAccountNotFound for hiddenID while delegating all other methods to base.
type stubAccountForGate struct {
	hiddenID string
	base     store.AccountStore
}

func (s *stubAccountForGate) GetAccount(ctx context.Context, id string) (*store.Account, error) {
	if id == s.hiddenID {
		return nil, store.ErrAccountNotFound
	}
	return s.base.GetAccount(ctx, id)
}
func (s *stubAccountForGate) CreateAccount(ctx context.Context, in store.AccountInput, op string) (*store.Account, error) {
	return s.base.CreateAccount(ctx, in, op)
}
func (s *stubAccountForGate) ListAccounts(ctx context.Context, st store.AccountStatus, o, l int) ([]*store.Account, error) {
	return s.base.ListAccounts(ctx, st, o, l)
}
func (s *stubAccountForGate) UpdateAccount(ctx context.Context, id string, upd store.AccountUpdate) (*store.Account, error) {
	return s.base.UpdateAccount(ctx, id, upd)
}
func (s *stubAccountForGate) DeleteAccountWithCascade(ctx context.Context, id string) (int64, error) {
	return s.base.DeleteAccountWithCascade(ctx, id)
}
func (s *stubAccountForGate) CountActiveAccountKeys(ctx context.Context, id string) (int64, error) {
	return s.base.CountActiveAccountKeys(ctx, id)
}
func (s *stubAccountForGate) ListAccountKeys(ctx context.Context, id string, o, l int) ([]*store.Key, error) {
	return s.base.ListAccountKeys(ctx, id, o, l)
}
func (s *stubAccountForGate) CreateKeyForAccount(ctx context.Context, aid, c, l string, e *time.Time) (*store.Key, error) {
	return s.base.CreateKeyForAccount(ctx, aid, c, l, e)
}

// TestAccounts_LegacyAccount_DeleteRejected verifies the legacy account
// cannot be deleted via the API.
func TestAccounts_LegacyAccount_DeleteRejected(t *testing.T) {
	h, _ := newAccountsHandlerWithStore(t)

	url := "/api/v1/accounts/legacy?confirm=legacy"
	req := chiAccountReq(httptest.NewRequest(http.MethodDelete, url, nil), "legacy")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 for legacy delete, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "ACCOUNT_RESERVED" {
		t.Errorf("want ACCOUNT_RESERVED, got %q", ae.Code)
	}
}

// TestAccounts_LegacyAccount_SuspendRejected verifies the legacy account
// cannot be suspended via PATCH.
func TestAccounts_LegacyAccount_SuspendRejected(t *testing.T) {
	h, _ := newAccountsHandlerWithStore(t)

	body, _ := json.Marshal(map[string]any{"status": "suspended"})
	req := chiAccountReq(httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/legacy", bytes.NewReader(body)), "legacy")
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 for legacy suspend, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "ACCOUNT_RESERVED" {
		t.Errorf("want ACCOUNT_RESERVED, got %q", ae.Code)
	}
}

// TestAccounts_IssueKey_SuspendedRejected verifies issue-key against a
// suspended account is rejected with 409 ACCOUNT_SUSPENDED.
func TestAccounts_IssueKey_SuspendedRejected(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "susp@x.com"}, "op")
	suspended := store.AccountStatusSuspended
	_, _ = s.UpdateAccount(ctx, a.ID, store.AccountUpdate{Status: &suspended})

	body := `{"component":"core","label":"k"}`
	req := chiAccountReq(httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+a.ID+"/keys", bytes.NewBufferString(body)), a.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.IssueKey(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "ACCOUNT_SUSPENDED" {
		t.Errorf("want ACCOUNT_SUSPENDED, got %q", ae.Code)
	}
}

// TestAccounts_IssueKey_BodyIDMismatchRejected verifies the URL {id} is
// authoritative and a mismatched body account_id is rejected.
func TestAccounts_IssueKey_BodyIDMismatchRejected(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	a, _ := s.CreateAccount(ctx, store.AccountInput{Email: "m@x.com"}, "op")

	body := `{"account_id":"other","component":"core","label":"k"}`
	req := chiAccountReq(httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+a.ID+"/keys", bytes.NewBufferString(body)), a.ID)
	req.Header.Set("Content-Type", "application/json")
	req = withOperatorRole(req, auth.RoleAdmin)
	w := httptest.NewRecorder()
	h.IssueKey(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

// TestAccounts_List_LinkHeaderPreservesFilters verifies that ?status=...
// survives pagination's next/prev links.
func TestAccounts_List_LinkHeaderPreservesFilters(t *testing.T) {
	h, s := newAccountsHandlerWithStore(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		a, _ := s.CreateAccount(ctx, store.AccountInput{Email: fmt.Sprintf("s%d@x.com", i)}, "op")
		susp := store.AccountStatusSuspended
		_, _ = s.UpdateAccount(ctx, a.ID, store.AccountUpdate{Status: &susp})
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?status=suspended&limit=2", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	link := w.Header().Get("Link")
	if !bytesContains(link, `status=suspended`) {
		t.Errorf("Link should preserve status=suspended filter, got %q", link)
	}
}

// TestKeys_List_LimitTooLarge verifies the missing § 3 pagination cap is
// now enforced on GET /api/v1/keys.
func TestKeys_List_LimitTooLarge(t *testing.T) {
	h := newTestKeysHandler(&mockStore{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys?limit=501", nil)
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
