/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";

import { App } from "./App";
import { setUnauthorizedHandler } from "./api/client";
import { LOGIN_PATH, handleUnauthorized } from "./api/unauthorized";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Admin data is short-lived (operators add/remove accounts often).
      // 30s stale time balances UI responsiveness with backend load.
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

// Wire the global 401 handler. The decision lives in api/unauthorized.ts
// (unit-tested); this supplies the effects. See that file for why a 401 from
// the session probe must not invalidate the session query (#209).
setUnauthorizedHandler((failedPath) =>
  handleUnauthorized(failedPath, window.location.pathname, {
    clearSession: () => queryClient.setQueryData(["session", "whoami"], null),
    invalidateSession: () => {
      void queryClient.invalidateQueries({ queryKey: ["session"] });
    },
    redirectToLogin: () => {
      window.location.href = LOGIN_PATH;
    },
  }),
);

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("admin-ui: #root element missing from index.html");
}

ReactDOM.createRoot(rootEl).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter basename="/admin">
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>,
);
