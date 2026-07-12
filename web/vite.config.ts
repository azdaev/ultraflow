import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The Go daemon serves the API + MCP on :7787. In dev we run Vite on :5173 and
// proxy /api (incl. the SSE stream) through to the daemon, so the frontend code
// uses same-origin relative URLs in both dev and the built (Go-served) app.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:7787",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
