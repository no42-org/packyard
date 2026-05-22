import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";

import { App } from "./App";
import { setUnauthorizedHandler } from "./api/client";
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

// Wire the global 401 handler: any API call returning 401 invalidates the
// cached operator (so App's logged-out branch renders), then forces a hard
// redirect to /admin/login so partially-loaded protected pages don't keep
// firing failing requests in the background.
setUnauthorizedHandler(() => {
  queryClient.setQueryData(["session", "whoami"], null);
  queryClient.invalidateQueries({ queryKey: ["session"] });
  if (window.location.pathname !== "/admin/login") {
    window.location.href = "/admin/login";
  }
});

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
