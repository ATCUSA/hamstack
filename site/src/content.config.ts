import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { z } from "astro/zod";

// The documentation is the repository's own docs/ folder, read in place: edit a file there and both GitHub and this
// site show it. The files have no frontmatter; the title is the first heading (see src/lib/docs.ts).
const docs = defineCollection({
  loader: glob({ pattern: "*.md", base: "../docs" }),
  schema: z.object({}).passthrough(),
});

export const collections = { docs };
