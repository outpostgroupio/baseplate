import { cloudflareTest } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.jsonc" },
      miniflare: {
        bindings: {
          SESSION_SECRET: "test-session-secret",
          ADMIN_TOKEN: "test-admin-token",
          GITHUB_CLIENT_ID: "gh-client",
          GITHUB_CLIENT_SECRET: "gh-secret",
          GITHUB_ORG: "acme",
          OIDC_REPOSITORIES: "acme/game",
        },
      },
    }),
  ],
});
