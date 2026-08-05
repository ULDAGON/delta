import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const deltaAPIToken = process.env.DELTA_API_TOKEN;

export default defineConfig({
  plugins: [react()],
  build: {
    // Keep the committed embed placeholder; generated files are ignored by git.
    emptyOutDir: false,
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
