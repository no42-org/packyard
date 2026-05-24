package store

import "strings"

// canonicalEmail returns the canonical form of an email address used for
// uniqueness checks and OAuth identity matching. Per design D19 of change
// 2026-05-21-admin-ui-account-lifecycle, canonicalisation is lowercase + trim;
// no RFC 5321 dot-handling, no +suffix stripping. Leading/trailing whitespace
// is removed because the operators/accounts CHECK constraints (email LIKE
// '%_@_%' AND email = lower(email)) would otherwise admit padded inputs and
// allow an allowlist row that the OAuth callback's canonical lookup can never
// match.
//
// All inserts and lookups against accounts.email and operators.email MUST
// route through this helper so the canonical form is the only form stored.
func canonicalEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
