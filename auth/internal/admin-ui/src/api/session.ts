import { useQuery } from "@tanstack/react-query";

import { ApiError, apiFetch } from "./client";

export interface CurrentOperator {
  id: string;
  email: string;
  role: "admin" | "readonly";
  status: "active" | "disabled";
}

// useCurrentOperator hits /api/v1/auth/whoami once at App mount. A 401 is
// treated as "logged out" — surfaced via `error` so App can redirect to the
// /login route without dragging React Router redirects into every hook.
export function useCurrentOperator() {
  return useQuery<CurrentOperator>({
    queryKey: ["session", "whoami"],
    queryFn: () => apiFetch<CurrentOperator>("/api/v1/auth/whoami"),
    retry: (failureCount, err) => {
      if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
        return false;
      }
      return failureCount < 2;
    },
    staleTime: 60_000,
  });
}
