import type { APIRoute } from "astro";
import { allDocs } from "../lib/docs";

// Generated, so a new file in docs/ is in the sitemap without anyone remembering to add it.
export const GET: APIRoute = async ({ site }) => {
  const base = site ?? new URL("https://hamstack.acole.dev");
  const paths = ["/", "/docs/", ...(await allDocs()).map((d) => `/docs/${d.slug}/`)];
  const body = `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${paths
    .map((p) => `  <url><loc>${new URL(p, base).href}</loc></url>`)
    .join("\n")}\n</urlset>\n`;
  return new Response(body, { headers: { "Content-Type": "application/xml" } });
};
