import { mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { stripTypes } from "./strip-types.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const outRoot = join(root, ".build");

function collect(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) {
      out.push(...collect(full));
    } else if (name.endsWith(".ts")) {
      out.push(full);
    }
  }
  return out;
}

rmSync(outRoot, { recursive: true, force: true });
const files = [...collect(join(root, "src")), ...collect(join(root, "test"))];
for (const file of files) {
  const src = readFileSync(file, "utf8");
  const out = stripTypes(src);
  const rel = relative(root, file).replace(/\.ts$/, ".js");
  const dest = join(outRoot, rel);
  mkdirSync(dirname(dest), { recursive: true });
  writeFileSync(dest, out);
}
console.log("stripped " + files.length + " files -> .build/");
