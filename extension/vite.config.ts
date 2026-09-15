import { defineConfig } from "vite";
import { resolve } from "node:path";
export default defineConfig({
  build: {
    rollupOptions: {
      input: {
        popup: resolve(import.meta.dirname, "popup.html"),
        background: resolve(import.meta.dirname, "src/background.ts"),
      },
      output: { entryFileNames: "[name].js" },
    },
    target: "es2022",
  },
});
