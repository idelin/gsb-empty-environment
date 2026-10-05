import { execFileSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
execFileSync(process.execPath, [join(root, "scripts/build.mjs")], { stdio: "inherit" });
await import(pathToFileURL(join(root, ".build/src/cli.js")).href);
