import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiFetch, apiListFetch, PaginatedResult } from "./client";

export type AccountStatus = "active" | "suspended" | "deleted";

export interface Account {
  id: string;
  email: string;
  org_name: string;
  status: AccountStatus;
  created_at: string;
  created_by_operator_id: string;
}

export interface Key {
  id: string;
  component: string;
  active: boolean;
  label: string;
  created_at: string;
  expires_at: string | null;
  usage_count: number;
  account_id: string;
}

export interface AccountsListParams {
  status?: AccountStatus;
  offset?: number;
  limit?: number;
}

export function useAccounts(params: AccountsListParams) {
  const qs = new URLSearchParams();
  if (params.status) qs.set("status", params.status);
  if (typeof params.offset === "number") qs.set("offset", String(params.offset));
  if (typeof params.limit === "number") qs.set("limit", String(params.limit));
  const query = qs.toString();
  return useQuery<PaginatedResult<Account>>({
    queryKey: ["accounts", "list", params],
    queryFn: () => apiListFetch<Account>(`/api/v1/accounts${query ? `?${query}` : ""}`),
  });
}

export function useAccount(id: string) {
  return useQuery<Account>({
    queryKey: ["accounts", "get", id],
    queryFn: () => apiFetch<Account>(`/api/v1/accounts/${encodeURIComponent(id)}`),
    enabled: !!id,
  });
}

export function useAccountKeys(id: string, params: { offset?: number; limit?: number }) {
  const qs = new URLSearchParams();
  if (typeof params.offset === "number") qs.set("offset", String(params.offset));
  if (typeof params.limit === "number") qs.set("limit", String(params.limit));
  const query = qs.toString();
  return useQuery<PaginatedResult<Key>>({
    queryKey: ["accounts", "keys", id, params],
    queryFn: () =>
      apiListFetch<Key>(
        `/api/v1/accounts/${encodeURIComponent(id)}/keys${query ? `?${query}` : ""}`,
      ),
    enabled: !!id,
  });
}

export function useCreateAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; org_name: string }) =>
      apiFetch<Account>("/api/v1/accounts", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["accounts", "list"] }),
  });
}

export function useUpdateAccount(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<{ email: string; org_name: string; status: AccountStatus }>) =>
      apiFetch<Account>(`/api/v1/accounts/${encodeURIComponent(id)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["accounts"] });
    },
  });
}

export function useDeleteAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, confirm }: { id: string; confirm: string }) =>
      apiFetch<{ keys_revoked: number }>(
        `/api/v1/accounts/${encodeURIComponent(id)}?confirm=${encodeURIComponent(confirm)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["accounts"] }),
  });
}

// IssuedKey is the response from POST /api/v1/accounts/{id}/keys. In Packyard
// the `id` IS the subscriber's secret (the HTTP Basic password they paste
// into yum/apt/docker config) — there is no separate secret field. The
// SPA's IssuedSecretModal renders `id` directly as the one-time reveal;
// re-fetching the key list shows the same id alongside metadata.
export type IssuedKey = Key & {
  component_visibility?: "public" | "private";
};

export function useIssueAccountKey(accountId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { component: string; label?: string; expires_at?: string | null }) =>
      apiFetch<IssuedKey>(
        `/api/v1/accounts/${encodeURIComponent(accountId)}/keys`,
        {
          method: "POST",
          body: JSON.stringify(body),
        },
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["accounts", "keys", accountId] }),
  });
}

export function useRevokeKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/api/v1/keys/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["accounts"] }),
  });
}
