import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiFetch } from "./client";

export interface Component {
  name: string;
  visibility: "public" | "private";
  rpm_series: string[];
  rpm_os_families: string[];
  rpm_architectures: string[];
  created_at: string;
}

export function useComponents() {
  return useQuery<Component[]>({
    queryKey: ["components", "list"],
    queryFn: () => apiFetch<Component[]>("/api/v1/components"),
  });
}

export function useCreateComponent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Omit<Component, "created_at">) =>
      apiFetch<Component>("/api/v1/components", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["components"] }),
  });
}

export function useUpdateComponent(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<Pick<Component, "visibility">>) =>
      apiFetch<Component>(`/api/v1/components/${encodeURIComponent(name)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["components"] }),
  });
}
