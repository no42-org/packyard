/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

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

// Fields PATCH /api/v1/components/{name} accepts. A list replaces the stored
// list; at least one field must be present.
export type ComponentPatch = Partial<
  Pick<Component, "visibility" | "rpm_series" | "rpm_os_families" | "rpm_architectures">
>;

// The updated record plus the series/family-arch targets the patch stopped
// declaring. Their directories stay on disk (operator responsibility).
export interface ComponentUpdateResult extends Component {
  rpm_targets_removed: string[];
}

export function useUpdateComponent(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ComponentPatch) =>
      apiFetch<ComponentUpdateResult>(`/api/v1/components/${encodeURIComponent(name)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["components"] }),
  });
}
