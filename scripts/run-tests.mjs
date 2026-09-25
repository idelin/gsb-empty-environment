import { spawnSync } from "node:child_process";
import { readdirSync } from "node:fs";

const [major, minor] = process.versions.node.split(".").map(Number);
const needsFlag = major < 22 || (major === 22 && minor < 18);
const args = [];
if (needsFlag) args.push("--experimental-strip-types");
args.push("--test");
for (const f of readdirSync("test").filter((f) => f.endsWith(".test.ts")).sort()) {
  args.push(`test/${f}`);
}
const r = spawnSync(process.execPath, args, { stdio: "inherit" });
process.exit(r.status ?? 1);
