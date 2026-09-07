/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// The safe-locked delete. DELETE /api/v1/components/{name} without ?confirm
// answers 409 CONFIRM_REQUIRED with an impact preview; the same call with
// ?confirm=<name> revokes the component's keys and removes the record. RPM
// directories stay on disk. The pure helpers here are unit-tested; the hooks
// wrap them for React Query.

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { ApiError, apiFetch } from "./client";

export interface DeleteImpact {
  keys_revoked: number;
  rpm_series_removed: string[];
}

export interface DeleteResult {
  keys_revoked: number;
}

// parseDeleteImpact turns the 409 the preview call throws into the impact
// object. Any other error is rethrown unchanged.
export function parseDeleteImpact(err: unknown): DeleteImpact {
  if (err instanceof ApiError && err.status === 409 && err.code === "CONFIRM_REQUIRED") {
    const impact = (err.payload as { impact?: Partial<DeleteImpact> } | null)?.impact ?? {};
    return {
      keys_revoked: Number(impact.keys_revoked ?? 0),
      rpm_series_removed: Array.isArray(impact.rpm_series_removed) ? impact.rpm_series_removed : [],
    };
  }
  throw err;
}

// canConfirmDelete gates the destructive button: the typed name must equal
// the component name exactly. The API enforces the same rule, case included.
export function canConfirmDelete(typed: string, name: string): boolean {
  return typed === name && name.length > 0;
}

export async function previewComponentDelete(name: string): Promise<DeleteImpact> {
  try {
    await apiFetch<unknown>(`/api/v1/components/${encodeURIComponent(name)}`, { method: "DELETE" });
  } catch (err) {
    return parseDeleteImpact(err);
  }
  // A 2xx here would mean the server deleted without confirmation, which the
  // API contract forbids. Treat it as an error so the UI never hides it.
  throw new Error(`DELETE ${name} without confirm did not return the impact preview`);
}

export function useDeleteComponent(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<DeleteResult>(
        `/api/v1/components/${encodeURIComponent(name)}?confirm=${encodeURIComponent(name)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["components"] }),
  });
}
