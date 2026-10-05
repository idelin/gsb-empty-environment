/**
 * CLI 入口：对内置示例构建执行审计。
 *
 * 用法:
 *   node src/cli.ts [--build flawed|sound] [--max-distance N] [--json]
 */

import { audit } from "./audit.ts";
import { flawedDemoBuild, soundDemoBuild } from "./examples.ts";
import { formatReport } from "./report.ts";

function parseArgs(argv: string[]): {
  build: string;
  maxDistance?: number;
  json: boolean;
} {
  const out: { build: string; maxDistance?: number; json: boolean } = {
    build: "flawed",
    json: false,
  };
  for (let i = 0; i < argv.length; i++) {
    switch (argv[i]) {
      case "--build":
        out.build = argv[++i];
        break;
      case "--max-distance":
        out.maxDistance = Number(argv[++i]);
        break;
      case "--json":
        out.json = true;
        break;
      default:
        console.error(`未知参数: ${argv[i]}`);
        process.exit(2);
    }
  }
  return out;
}

const args = parseArgs(process.argv.slice(2));
const spec = args.build === "sound" ? soundDemoBuild : flawedDemoBuild;
const result = audit(spec, { maxDistance: args.maxDistance });

if (args.json) {
  console.log(JSON.stringify(result, null, 2));
} else {
  console.log(formatReport(spec, result));
}
