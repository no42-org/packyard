/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import { describe, expect, it, vi } from "vitest";

import {
  LOGIN_PATH,
  SESSION_PROBE_PATH,
  handleUnauthorized,
  isSessionProbe,
} from "./unauthorized";

function effects() {
  return {
    clearSession: vi.fn(),
    invalidateSession: vi.fn(),
    redirectToLogin: vi.fn(),
  };
}

describe("handleUnauthorized", () => {
  it("does nothing on the login page, so a failing probe cannot loop", () => {
    const e = effects();
    handleUnauthorized(SESSION_PROBE_PATH, LOGIN_PATH, e);
    expect(e.clearSession).not.toHaveBeenCalled();
    expect(e.invalidateSession).not.toHaveBeenCalled();
    expect(e.redirectToLogin).not.toHaveBeenCalled();
  });

  it("never invalidates the session query for a 401 from the probe itself", () => {
    const e = effects();
    handleUnauthorized(SESSION_PROBE_PATH, "/admin/accounts", e);
    expect(e.clearSession).toHaveBeenCalledTimes(1);
    expect(e.invalidateSession).not.toHaveBeenCalled();
    expect(e.redirectToLogin).toHaveBeenCalledTimes(1);
  });

  it("clears, re-confirms once and redirects for a 401 from a data call", () => {
    const e = effects();
    handleUnauthorized("/api/v1/accounts?page=2", "/admin/accounts", e);
    expect(e.clearSession).toHaveBeenCalledTimes(1);
    expect(e.invalidateSession).toHaveBeenCalledTimes(1);
    expect(e.redirectToLogin).toHaveBeenCalledTimes(1);
  });

  it("settles after a bounded number of steps when the probe keeps failing", () => {
    // Simulate the React Query feedback: every invalidation refetches the
    // probe, which answers 401 and re-enters the handler. With the guard the
    // chain is data-call -> probe -> stop.
    const e = effects();
    let entries = 0;
    const invalidate = () => {
      entries += 1;
      if (entries > 10) throw new Error("loop");
      handleUnauthorized(SESSION_PROBE_PATH, "/admin/accounts", { ...e, invalidateSession: invalidate });
    };
    handleUnauthorized("/api/v1/keys", "/admin/accounts", { ...e, invalidateSession: invalidate });
    expect(entries).toBe(1);
  });
});

describe("isSessionProbe", () => {
  it("matches the probe path with or without a query string", () => {
    expect(isSessionProbe(SESSION_PROBE_PATH)).toBe(true);
    expect(isSessionProbe(`${SESSION_PROBE_PATH}?t=1`)).toBe(true);
    expect(isSessionProbe("/api/v1/auth/whoami-else")).toBe(false);
    expect(isSessionProbe("/api/v1/accounts")).toBe(false);
  });
});
