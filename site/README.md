# hamstack website

The landing page at <https://hamstack.acole.dev>, built with [Astro](https://astro.build) as a fully static site and served by a
Cloudflare Worker using [static assets](https://developers.cloudflare.com/workers/static-assets/) (no server code).

```sh
task site:install     # pnpm install --frozen-lockfile
task site:dev         # http://localhost:4321 with live reload
task site:build       # -> site/dist
task site:deploy      # build, then wrangler deploy
```

## The docs section

`/docs/` renders the repository's own [`docs/`](../docs) folder, read in place (an Astro content collection with the `glob` loader; see
`src/content.config.ts`). Nothing is copied: edit `docs/foo.md` and both GitHub and the site show it. A new file appears on the site by itself.

- The page title is the file's first `# heading`. Order and grouping are in `src/lib/docs.ts` (a file not listed there appears under "More").
- Links between docs are written the way GitHub wants them (`packet.md`, `packet.md#kernel`); the site rewrites them to `/docs/packet/`.
  Any other relative link (`../README.md`, `../scripts/install.sh`) becomes a link to that file on GitHub. See `src/plugins/docs-markdown.mjs`.
- Headings get GitHub-style ids and a `#` anchor; code is highlighted at build time (no script).
- The table of contents, the sidebar, previous/next and the sitemap are all generated.
- After changing the plugin or the content config, clear Astro's content cache: `rm -rf site/.astro site/node_modules/.astro`.

## Where things are

| | |
|---|---|
| `src/pages/index.astro` | the page: copy, the feature list, the programs list and the "where to post" links are arrays at the top |
| `src/styles/global.css` | the whole look: the colour tokens at the top ("amber shack": dark by default, warm paper in light mode) |
| `src/pages/docs/` | the docs index and the page template for each doc |
| `src/layouts/DocsLayout.astro`, `src/styles/docs.css` | the docs chrome (sidebar, table of contents, prose styles) |
| `src/layouts/Base.astro` | the `<head>`: title, description, social tags, canonical URL |
| `public/_headers` | security headers and caching (the Content-Security-Policy only allows this site's own scripts) |
| `wrangler.jsonc` | the Worker: serves `./dist`, and binds the custom domain |

## Deploying

The domain's zone has to be on the Cloudflare account you deploy to, and wrangler has to be logged in (`pnpm exec wrangler login`).
Choose the account with an environment variable rather than committing its id:

```sh
CLOUDFLARE_ACCOUNT_ID=<account id> task site:deploy
```

To use another domain, change `routes` in `wrangler.jsonc` (and `site` in `astro.config.mjs`).

## Notes

- pnpm only runs install scripts for `esbuild` and `workerd` (see `pnpm-workspace.yaml`); `sharp` is denied because the site has no images.
- The page makes one request to GitHub's public API (the latest release shown on the Releases button) and nothing else; it works without it.
- Astro 7 needs Node 22.23.4 or newer; the repository's `mise.toml` pins Node 26 (`mise install`), which is also what CI uses.
