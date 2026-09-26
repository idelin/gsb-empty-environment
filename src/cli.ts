// Usage:
//   node src/cli.ts [--retract <sampleId>]... [--compact] <file.json>...
// Each input file is one sample; its id is the file basename.

import { readFileSync } from "node:fs";
import { basename } from "node:path";
import { SampleStore } from "./evidence.ts";
import { buildReport } from "./report.ts";

function main(argv: string[]): number {
  const files: string[] = [];
  const retractions: string[] = [];
  let pretty = true;
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--retract") {
      const id = argv[++i];
      if (!id) return usage("missing value for --retract");
      retractions.push(id);
    } else if (arg === "--compact") {
      pretty = false;
    } else if (arg === "--help" || arg === "-h") {
      return usage();
    } else if (arg.startsWith("--")) {
      return usage(`unknown flag: ${arg}`);
    } else {
      files.push(arg);
    }
  }

  const store = new SampleStore();
  for (const file of files) {
    const id = basename(file).replace(/\.json$/, "");
    let value: unknown;
    try {
      value = JSON.parse(readFileSync(file, "utf8"));
    } catch (err) {
      console.error(`cannot read sample ${file}: ${(err as Error).message}`);
      return 2;
    }
    store.add(id, value);
  }
  for (const id of retractions) {
    if (!store.retract(id)) console.error(`warning: no such sample to retract: ${id}`);
  }

  const report = buildReport(store);
  console.log(JSON.stringify(report, null, pretty ? 2 : 0));
  return 0;
}

function usage(err?: string): number {
  if (err) console.error(`error: ${err}`);
  console.error("usage: node src/cli.ts [--retract <sampleId>]... [--compact] <file.json>...");
  return err ? 2 : 0;
}

process.exit(main(process.argv.slice(2)));
