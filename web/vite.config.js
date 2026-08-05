import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const deltaAPIToken = process.env.DELTA_API_TOKEN;

export default defineConfig({
  plugins: [react()],
  build: {
    // dist is committed for `go install` embedding; clear stale hashed assets.
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": {
        target: process.env.DELTA_API_URL || "http://127.0.0.1:7331",
        changeOrigin: false,
        headers: deltaAPIToken ? { Authorization: `Bearer ${deltaAPIToken}` } : undefined,
      },
    },
  },
});
