import { readdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

async function visit(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await visit(path);
      continue;
    }
    if (!entry.name.endsWith(".d.ts")) continue;
    const source = await readFile(path, "utf8");
    const rewritten = source.replace(/\.ts(["'])/g, ".js$1");
    if (rewritten !== source) await writeFile(path, rewritten);
  }
}

await visit(fileURLToPath(new URL("../dist", import.meta.url)));
