/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package store

import (
	"context"
	"testing"
	"time"

	"github.com/no42-org/packyard-auth/internal/audit"
)

func writeTestAudit(t *testing.T, s *SQLiteStore, e audit.Entry) {
	t.Helper()
	s.Write(context.Background(), e)
}

func TestAuditStore_WriteAndListRoundTrip(t *testing.T) {
	s := newTestStore(t)
	writeTestAudit(t, s, audit.Entry{
		OperatorID: "op-1",
		Action:     "account.create",
		TargetType: "account",
		TargetID:   "acct-abc",
		Details:    map[string]any{"email": "a@x.com"},
		IP:         "1.2.3.4",
		UserAgent:  "go-test",
	})

	got, err := s.ListAuditEntries(context.Background(), AuditFilter{}, 0, 50)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	if e.OperatorID != "op-1" || e.Action != "account.create" || e.TargetID != "acct-abc" {
		t.Errorf("round-trip fields: %+v", e)
	}
	if e.Details["email"] != "a@x.com" {
		t.Errorf("details json round-trip: %v", e.Details)
	}
}

func TestAuditStore_FilterByOperator(t *testing.T) {
	s := newTestStore(t)
	writeTestAudit(t, s, audit.Entry{OperatorID: "op-a", Action: "x"})
	writeTestAudit(t, s, audit.Entry{OperatorID: "op-b", Action: "x"})
	got, _ := s.ListAuditEntries(context.Background(), AuditFilter{OperatorID: "op-a"}, 0, 50)
	if len(got) != 1 || got[0].OperatorID != "op-a" {
		t.Errorf("operator filter: want one op-a row, got %+v", got)
	}
}

func TestAuditStore_FilterByAction(t *testing.T) {
	s := newTestStore(t)
	writeTestAudit(t, s, audit.Entry{Action: "login.success"})
	writeTestAudit(t, s, audit.Entry{Action: "login.failure"})
	writeTestAudit(t, s, audit.Entry{Action: "login.success"})
	got, _ := s.ListAuditEntries(context.Background(), AuditFilter{Action: "login.success"}, 0, 50)
	if len(got) != 2 {
		t.Errorf("action filter: want 2 login.success, got %d", len(got))
	}
}

func TestAuditStore_FilterByTargetTypeAndID(t *testing.T) {
	s := newTestStore(t)
	writeTestAudit(t, s, audit.Entry{Action: "account.create", TargetType: "account", TargetID: "a1"})
	writeTestAudit(t, s, audit.Entry{Action: "key.issue", TargetType: "key", TargetID: "k1"})
	got, _ := s.ListAuditEntries(context.Background(),
		AuditFilter{TargetType: "key", TargetID: "k1"}, 0, 50)
	if len(got) != 1 || got[0].Action != "key.issue" {
		t.Errorf("target_type+target_id filter: %+v", got)
	}
}

func TestAuditStore_FilterByTimeWindow(t *testing.T) {
	s := newTestStore(t)
	// Insert three rows; we can't control ts directly (SQLite default), but
	// we can use Since/Until with carefully chosen bounds around "now".
	writeTestAudit(t, s, audit.Entry{Action: "first"})
	writeTestAudit(t, s, audit.Entry{Action: "second"})
	writeTestAudit(t, s, audit.Entry{Action: "third"})

	// since = far past should return all 3.
	since := time.Unix(0, 0)
	got, _ := s.ListAuditEntries(context.Background(), AuditFilter{Since: &since}, 0, 50)
	if len(got) != 3 {
		t.Errorf("since=epoch: want 3, got %d", len(got))
	}

	// until = far past should return 0.
	until := time.Unix(0, 0)
	got, _ = s.ListAuditEntries(context.Background(), AuditFilter{Until: &until}, 0, 50)
	if len(got) != 0 {
		t.Errorf("until=epoch: want 0, got %d", len(got))
	}
}

func TestAuditStore_OrderedTsDesc(t *testing.T) {
	s := newTestStore(t)
	writeTestAudit(t, s, audit.Entry{Action: "first"})
	writeTestAudit(t, s, audit.Entry{Action: "second"})
	writeTestAudit(t, s, audit.Entry{Action: "third"})
	got, _ := s.ListAuditEntries(context.Background(), AuditFilter{}, 0, 50)
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	// id-DESC tie-break ensures stable ordering with second-precision ts:
	// most recent insert is first.
	if got[0].Action != "third" || got[2].Action != "first" {
		t.Errorf("ordering: want third/second/first, got %s/%s/%s",
			got[0].Action, got[1].Action, got[2].Action)
	}
}

func TestAuditStore_Pagination(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 10; i++ {
		writeTestAudit(t, s, audit.Entry{Action: "x"})
	}
	page1, _ := s.ListAuditEntries(context.Background(), AuditFilter{}, 0, 3)
	if len(page1) != 3 {
		t.Errorf("page 1: want 3, got %d", len(page1))
	}
	page2, _ := s.ListAuditEntries(context.Background(), AuditFilter{}, 3, 3)
	if len(page2) != 3 {
		t.Errorf("page 2: want 3, got %d", len(page2))
	}
	pageBeyond, _ := s.ListAuditEntries(context.Background(), AuditFilter{}, 100, 3)
	if len(pageBeyond) != 0 {
		t.Errorf("offset past end: want 0, got %d", len(pageBeyond))
	}
}

func TestAuditStore_WriteWithEmptyOptionalsStoresNull(t *testing.T) {
	s := newTestStore(t)
	writeTestAudit(t, s, audit.Entry{Action: "x"}) // no operator, no target, no details
	got, _ := s.ListAuditEntries(context.Background(), AuditFilter{}, 0, 50)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	e := got[0]
	if e.OperatorID != "" || e.TargetType != "" || e.TargetID != "" || e.IP != "" || e.UserAgent != "" {
		t.Errorf("empty optionals should round-trip as empty strings, got %+v", e)
	}
	if e.Details != nil {
		t.Errorf("empty details should round-trip as nil map, got %v", e.Details)
	}
}

func TestAuditStore_FireAndForgetOnPersistFailure(t *testing.T) {
	s := newTestStore(t)
	s.Close() // force failure on next write
	// Write must not panic and must return normally.
	s.Write(context.Background(), audit.Entry{Action: "after-close"})
}
