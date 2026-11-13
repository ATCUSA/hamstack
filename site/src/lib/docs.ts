import { getCollection, render, type CollectionEntry } from "astro:content";

// The reading order and grouping of the docs. A file in docs/ that is not listed here still appears, under "More", so a new
// page needs no change to the site.
export const groups: { title: string; slugs: string[] }[] = [
  { title: "Using hamstack", slugs: ["interface", "radios-and-sites", "hardware-and-services", "programs", "packet", "gps", "network"] },
  { title: "Testing and helping", slugs: ["testing", "hardware-testing"] },
  { title: "Under the hood", slugs: ["architecture"] },
];

// One line under each title on the docs index. A page without one simply shows none.
export const blurbs: Record<string, string> = {
  interface: "The terminal interface: its tabs and keys.",
  "radios-and-sites": "Rigs, choosing a radio, sites (home, the EOC, the club) and callsigns.",
  "hardware-and-services": "Finding your radio's USB devices, permissions, and what runs as a service.",
  programs: "The program catalog, dependencies, building from source, and keeping everything current.",
  packet: "Direwolf, watching and calling on packet, and the Linux AX.25 stack.",
  gps: "gpsd as a service, and your grid square from a GPS fix.",
  network: "Every port the stack uses and who on your network can reach it.",
  testing: "How hamstack is tested, from unit tests to real systemd in containers.",
  "hardware-testing": "How to try it with a real radio, and send a report that helps.",
  architecture: "How the pieces fit together, for people changing the code.",
};

export type Doc = { slug: string; title: string; entry: CollectionEntry<"docs"> };

// The title of a page is its first heading.
export async function titleOf(entry: CollectionEntry<"docs">): Promise<string> {
  const { headings } = await render(entry);
  return headings.find((h) => h.depth === 1)?.text ?? entry.id;
}

// Every page in reading order.
export async function allDocs(): Promise<Doc[]> {
  const entries = await getCollection("docs");
  const bySlug = new Map(entries.map((e) => [e.id, e]));
  const listed = groups.flatMap((g) => g.slugs).filter((s) => bySlug.has(s));
  const extra = entries.map((e) => e.id).filter((s) => !listed.includes(s)).sort();
  return Promise.all(
    [...listed, ...extra].map(async (slug) => {
      const entry = bySlug.get(slug)!;
      return { slug, title: await titleOf(entry), entry };
    }),
  );
}

// The sidebar: the groups, with anything unlisted under "More".
export async function navGroups(): Promise<{ title: string; docs: Doc[] }[]> {
  const docs = await allDocs();
  const by = new Map(docs.map((d) => [d.slug, d]));
  const out = groups.map((g) => ({ title: g.title, docs: g.slugs.map((s) => by.get(s)).filter((d): d is Doc => !!d) }));
  const used = new Set(groups.flatMap((g) => g.slugs));
  const more = docs.filter((d) => !used.has(d.slug));
  if (more.length) out.push({ title: "More", docs: more });
  return out.filter((g) => g.docs.length);
}
