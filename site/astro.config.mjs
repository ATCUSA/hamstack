import { readdirSync } from "node:fs";
import { defineConfig } from "astro/config";
import { satteri, satteriHeadingIdsPlugin } from "@astrojs/markdown-satteri";
import docsMarkdown from "./src/plugins/docs-markdown.mjs";

// The docs are the repository's docs/ folder; their file names are the page slugs.
const docSlugs = readdirSync(new URL("../docs/", import.meta.url))
  .filter((f) => f.endsWith(".md"))
  .map((f) => f.slice(0, -3));

// A fully static site: no adapter and no server code. Wrangler serves ./dist as Workers static assets.
export default defineConfig({
  site: "https://hamstack.acole.dev",
  output: "static",
  compressHTML: true,
  build: {
    // One small page: inline the stylesheet so there is no render-blocking request.
    inlineStylesheets: "always",
  },
  markdown: {
    // Code blocks are highlighted at build time (no script) and stay dark in both themes, like a terminal.
    shikiConfig: { theme: "github-dark-dimmed" },
    processor: satteri({
      // Astro's heading ids first, so the docs plugin can anchor to them.
      hastPlugins: [satteriHeadingIdsPlugin(), docsMarkdown({ slugs: docSlugs, repo: "https://github.com/ATCUSA/hamstack" })],
    }),
  },
  vite: {
    // Keep scripts as files, never inlined into the HTML, so the Content-Security-Policy in public/_headers can say script-src 'self'.
    build: { assetsInlineLimit: 0 },
  },
});
