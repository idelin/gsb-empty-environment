import { spawnSync } from "node:child_process";

const [major, minor] = process.versions.node.split(".").map(Number);
const needsFlag = major < 22 || (major === 22 && minor < 18);
const args = [];
if (needsFlag) args.push("--experimental-strip-types");
args.push("examples/demo.ts", ...process.argv.slice(2));
const r = spawnSync(process.execPath, args, { stdio: "inherit" });
process.exit(r.status ?? 1);
