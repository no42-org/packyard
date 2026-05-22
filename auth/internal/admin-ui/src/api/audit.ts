import { useQuery } from "@tanstack/react-query";

import { apiListFetch, PaginatedResult } from "./client";

export interface AuditEntry {
  id: number;
  ts: string;
  operator_id?: string;
  action: string;
  target_type?: string;
  target_id?: string;
  details?: Record<string, unknown>;
  ip?: string;
  user_agent?: string;
}

export interface AuditFilters {
  operator?: string;
  action?: string;
  target_type?: string;
  target_id?: string;
  since?: string;
  until?: string;
  offset?: number;
  limit?: number;
}

export function useAuditLog(filters: AuditFilters) {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(filters)) {
    if (v === undefined || v === "") continue;
    qs.set(k, String(v));
  }
  const query = qs.toString();
  return useQuery<PaginatedResult<AuditEntry>>({
    queryKey: ["audit", "list", filters],
    queryFn: () => apiListFetch<AuditEntry>(`/api/v1/audit${query ? `?${query}` : ""}`),
  });
}
