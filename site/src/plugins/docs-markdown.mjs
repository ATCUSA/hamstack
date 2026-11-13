// A Sätteri (Astro 7's Markdown processor) plugin for the docs, so markdown written for GitHub also works on the site:
//  - relative links to other docs ("packet.md#kernel") become site links ("/docs/packet/#kernel");
//  - other relative links ("../README.md", "../scripts/install.sh") point at the file on GitHub;
//  - h2 to h4 headings get a "#" anchor link (the ids come from Astro's own heading-id plugin, which runs first).
import { defineHastPlugin } from "satteri";

export default function docsMarkdown({ slugs = [], repo = "" } = {}) {
  const known = new Set(slugs);

  function rewrite(href) {
    if (/^([a-z][a-z0-9+.-]*:|\/|#)/i.test(href)) return href; // absolute URL, site path, or in-page anchor
    const [pathPart, hash = ""] = href.split(/(?=#)/);
    const path = pathPart.replace(/^\.\//, "");

    const doc = path.match(/^(?:docs\/)?([a-z0-9-]+)\.md$/i);
    if (doc && known.has(doc[1])) return `/docs/${doc[1]}/${hash}`;

    // Anything else relative lives in the repository: link to it on GitHub.
    const repoPath = path.replace(/^(\.\.\/)+/, "");
    const kind = /\.[a-z0-9]+$/i.test(repoPath) ? "blob" : "tree";
    return `${repo}/${kind}/main/${repoPath.replace(/\/$/, "")}${hash}`;
  }

  return defineHastPlugin({
    name: "hamstack-docs-markdown",
    element: [
      {
        filter: ["a"],
        visit(node, ctx) {
          const href = node.properties?.href;
          if (typeof href === "string") ctx.setProperty(node, "href", rewrite(href));
        },
      },
      {
        filter: ["h2", "h3", "h4"],
        visit(node, ctx) {
          const id = node.properties?.id;
          if (!id) return;
          ctx.appendChild(node, {
            type: "element",
            tagName: "a",
            // No text: the "#" is drawn by CSS, so it is not part of the heading's text (which feeds the table of contents).
            properties: { className: ["anchor"], href: `#${id}`, ariaLabel: "Link to this section" },
            children: [],
          });
        },
      },
    ],
  });
}
