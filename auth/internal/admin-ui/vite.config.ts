import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite config tuned for embedding into the auth Go binary per D12 + D22.
//
// - `base: "/admin/"` makes all bundled asset URLs absolute under /admin/,
//   matching how the Go handler serves them via go:embed.
// - `build.assetsDir: "assets"` so bundled JS/CSS end up at
//   /admin/assets/*.js | *.css (referenced by `<script src>` / `<link href>`).
// - CSS is extracted into a side-file, not inlined — required for the CSP
//   policy in D22 (`style-src 'self' 'nonce-{nonce}'`) to authorise bundled
//   styles via 'self' instead of needing 'unsafe-inline'.
// - No `legacy()` plugin: it injects an inline polyfill script that would
//   need an 'unsafe-inline' hatch. Admin UI ships modern-browser-only.
export default defineConfig({
  base: "/admin/",
  plugins: [react()],
  build: {
    outDir: "dist",
    assetsDir: "assets",
    cssCodeSplit: false,
    sourcemap: false,
    rollupOptions: {
      output: {
        // Stable file names with content hash so the Go handler's index.html
        // template doesn't need to know about per-build chunk names.
        entryFileNames: "assets/[name]-[hash].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]",
      },
    },
  },
  server: {
    proxy: {
      "/api/v1": "http://localhost:8080",
    },
  },
});
