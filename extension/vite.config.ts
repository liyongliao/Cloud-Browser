import { defineConfig } from "vite";
import { resolve } from "node:path";

export default defineConfig({
  build: {
    rollupOptions: {
      input: {
        popup: resolve(import.meta.dirname, "popup.html"),
        background: resolve(import.meta.dirname, "src/background.ts"),
        content: resolve(import.meta.dirname, "src/content.ts"),
        offscreen: resolve(import.meta.dirname, "offscreen.html"),
      },
      output: {
        entryFileNames: "[name].js",
      },
    },
    target: "es2022",
  },
});
