import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiFetch, apiListFetch, PaginatedResult } from "./client";

export type OperatorRole = "admin" | "readonly";
export type OperatorStatus = "active" | "disabled";

export interface Operator {
  id: string;
  email: string;
  role: OperatorRole;
  status: OperatorStatus;
  allowlisted_at: string;
  allowlisted_by: string;
  last_login_at?: string;
  github_username?: string;
  microsoft_upn?: string;
  first_seen_provider?: string;
}

export function useOperators(params: { offset?: number; limit?: number }) {
  const qs = new URLSearchParams();
  if (typeof params.offset === "number") qs.set("offset", String(params.offset));
  if (typeof params.limit === "number") qs.set("limit", String(params.limit));
  const query = qs.toString();
  return useQuery<PaginatedResult<Operator>>({
    queryKey: ["operators", "list", params],
    queryFn: () => apiListFetch<Operator>(`/api/v1/operators${query ? `?${query}` : ""}`),
  });
}

export function useAddOperator() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; role: OperatorRole }) =>
      apiFetch<Operator>("/api/v1/operators", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["operators"] }),
  });
}

export function useUpdateOperator(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<{ role: OperatorRole; status: OperatorStatus }>) =>
      apiFetch<Operator>(`/api/v1/operators/${encodeURIComponent(id)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["operators"] });
      // If the actor mutated themselves (role demote, self-disable), the
      // chrome's whoami cache is now stale. Re-fetch so the nav re-renders
      // with the new role and the post-demote 401 handler triggers if the
      // server force-deleted our session.
      qc.invalidateQueries({ queryKey: ["session"] });
    },
  });
}
