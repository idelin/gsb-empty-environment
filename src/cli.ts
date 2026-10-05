// CLI 入口：
//   node src/cli.ts --attrs ABCDE --fds "A->BC;BC->D" --decomp "ABC CDE"
//   node src/cli.ts --file spec.json        # {"attributes":"...","fds":"...","decomposition":"..."}
//   加 --json 输出机器可读结果（用于重放校验）。
import { readFileSync } from "node:fs";
import { buildSpec, ParseError } from "./parser.ts";
import { check } from "./checker.ts";
import { renderReport } from "./report.ts";

interface Args {
  attrs?: string;
  fds?: string;
  decomp?: string;
  file?: string;
  json: boolean;
}

function parseArgs(argv: string[]): Args {
  const args: Args = { json: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--json") args.json = true;
    else if (a === "--attrs") args.attrs = argv[++i];
    else if (a === "--fds") args.fds = argv[++i];
    else if (a === "--decomp") args.decomp = argv[++i];
    else if (a === "--file") args.file = argv[++i];
    else throw new ParseError(`未知参数: ${a}`);
  }
  return args;
}

function main(): void {
  const args = parseArgs(process.argv.slice(2));
  let attrs = args.attrs;
  let fds = args.fds;
  let decomp = args.decomp;
  if (args.file) {
    const spec = JSON.parse(readFileSync(args.file, "utf8"));
    attrs = attrs ?? spec.attributes;
    fds = fds ?? spec.fds;
    decomp = decomp ?? spec.decomposition;
  }
  if (!attrs || !fds || !decomp) {
    throw new ParseError(
      "缺少输入。用法: node src/cli.ts --attrs ABCDE --fds \"A->BC;BC->D\" --decomp \"ABC CDE\" [--json]"
    );
  }
  const report = check(buildSpec(attrs, fds, decomp));
  if (args.json) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    console.log(renderReport(report));
  }
  // 退出码：0 = 无损且保持；1 = 有损或不保持；2 = 输入错误
  process.exitCode = report.chase.lossless && report.dependenciesPreserved ? 0 : 1;
}

try {
  main();
} catch (e) {
  if (e instanceof ParseError) {
    console.error(`输入错误: ${e.message}`);
    process.exitCode = 2;
  } else {
    throw e;
  }
}
