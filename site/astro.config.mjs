import { defineConfig } from "astro/config";

// A fully static site: no adapter and no server code. Wrangler serves ./dist as Workers static assets.
export default defineConfig({
  site: "https://hamstack.acole.dev",
  output: "static",
  compressHTML: true,
  build: {
    // One small page: inline the stylesheet so there is no render-blocking request.
    inlineStylesheets: "always",
  },
  vite: {
    // Keep scripts as files, never inlined into the HTML, so the Content-Security-Policy in public/_headers can say script-src 'self'.
    build: { assetsInlineLimit: 0 },
  },
});
