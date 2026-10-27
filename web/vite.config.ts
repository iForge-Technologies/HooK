import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// In dev, the hub runs on :8080 and Vite proxies the API (including SSE) to it.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: { port: 5173, proxy: { "/api": { target: "http://127.0.0.1:8080", changeOrigin: false } } },
  build: { outDir: "dist", emptyOutDir: true, target: "es2022" },
});
