import { fileURLToPath } from "node:url";
import { defineConfig, lazyPlugins } from "vite-plus";
import react from "@vitejs/plugin-react";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import tailwindcss from "@tailwindcss/vite";

// https://vite.dev/config/
export default defineConfig({
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
        configure: (proxy) => {
          // The backend deliberately enforces same-origin mutations. Rewrite
          // the browser origin only inside this local development proxy.
          proxy.on("proxyReq", (request) => {
            request.setHeader("Origin", "http://127.0.0.1:8080");
          });
        },
      },
    },
  },
  fmt: {
    ignorePatterns: ["src/routeTree.gen.ts"],
  },
  test: {
    exclude: ["**/node_modules/**", "**/dist/**", "e2e/**"],
  },
  lint: {
    plugins: ["react", "typescript", "oxc"],
    rules: {
      "react/rules-of-hooks": "error",
      // TanStack Router route modules intentionally export both a route and
      // its component, which is incompatible with this Fast Refresh rule.
      "react/only-export-components": "off",
      "vite-plus/prefer-vite-plus-imports": "error",
    },
    options: {
      typeAware: true,
      typeCheck: true,
    },
    jsPlugins: [
      {
        name: "vite-plus",
        specifier: "vite-plus/oxlint-plugin",
      },
    ],
  },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  plugins: lazyPlugins(() => [
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
    }),
    react(),
    tailwindcss(),
  ]),
});
