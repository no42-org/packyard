/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// What the SPA does when any API call answers 401. Kept free of React and
// browser globals so the decision can be unit-tested; main.tsx supplies the
// effects.
//
// The trap this guards against: the session probe (whoami) fails with 401,
// the handler invalidates the session query, the mounted query has
// staleTime 0 and refetches at once, which fails with 401 again. On a
// logged-out login page that loop ran at roughly ten requests per second
// (issue #209). Two rules stop it:
//
//   1. On /admin/login there is nothing to do: App already renders the
//      logged-out branch from the probe's own error.
//   2. A 401 from the probe itself clears the cached operator but never
//      invalidates the session query; only a 401 from a data call does, so
//      the probe re-confirms the state exactly once.

export const SESSION_PROBE_PATH = "/api/v1/auth/whoami";
export const LOGIN_PATH = "/admin/login";

export interface UnauthorizedEffects {
  /** Drop the cached operator so App renders the logged-out branch. */
  clearSession: () => void;
  /** Mark the session query stale so it re-confirms on next render. */
  invalidateSession: () => void;
  /** Hard-navigate to the login route. */
  redirectToLogin: () => void;
}

export function handleUnauthorized(
  failedPath: string,
  currentPathname: string,
  effects: UnauthorizedEffects,
): void {
  if (currentPathname === LOGIN_PATH) {
    return;
  }
  effects.clearSession();
  if (!isSessionProbe(failedPath)) {
    effects.invalidateSession();
  }
  effects.redirectToLogin();
}

export function isSessionProbe(path: string): boolean {
  // Strip a query string or fragment; compare the path only.
  const bare = path.split(/[?#]/, 1)[0];
  return bare === SESSION_PROBE_PATH;
}
