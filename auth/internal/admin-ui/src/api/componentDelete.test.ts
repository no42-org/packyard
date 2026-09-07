/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import { describe, expect, it } from "vitest";

import { ApiError } from "./client";
import { canConfirmDelete, parseDeleteImpact } from "./componentDelete";

describe("parseDeleteImpact", () => {
  it("extracts the impact from the 409 CONFIRM_REQUIRED envelope", () => {
    const err = new ApiError(
      409,
      {
        code: "CONFIRM_REQUIRED",
        message: "Deleting will revoke keys",
        impact: { keys_revoked: 3, rpm_series_removed: ["38", "2025"] },
      },
      "HTTP 409",
    );
    expect(parseDeleteImpact(err)).toEqual({ keys_revoked: 3, rpm_series_removed: ["38", "2025"] });
  });

  it("tolerates a preview with missing fields", () => {
    const err = new ApiError(409, { code: "CONFIRM_REQUIRED", message: "x" }, "HTTP 409");
    expect(parseDeleteImpact(err)).toEqual({ keys_revoked: 0, rpm_series_removed: [] });
  });

  it("rethrows anything that is not the preview", () => {
    const notFound = new ApiError(404, { code: "COMPONENT_NOT_FOUND", message: "nope" }, "HTTP 404");
    expect(() => parseDeleteImpact(notFound)).toThrow(notFound);
    const network = new Error("offline");
    expect(() => parseDeleteImpact(network)).toThrow(network);
  });
});

describe("canConfirmDelete", () => {
  it("requires the exact name, case included", () => {
    expect(canConfirmDelete("bluebird", "bluebird")).toBe(true);
    expect(canConfirmDelete("Bluebird", "bluebird")).toBe(false);
    expect(canConfirmDelete("bluebird ", "bluebird")).toBe(false);
    expect(canConfirmDelete("", "bluebird")).toBe(false);
    expect(canConfirmDelete("", "")).toBe(false);
  });
});
