import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5188,
    strictPort: true,
    proxy: {
      "/api": "http://127.0.0.1:8188",
      "/control": "http://127.0.0.1:8188",
      "/view": { target: "http://127.0.0.1:8188", ws: true },
      "/audio": { target: "http://127.0.0.1:8188", ws: true },
    },
  },
});
