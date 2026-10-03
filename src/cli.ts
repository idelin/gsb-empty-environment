import { HASH_REGISTRY } from "./hashes.ts";
import { KEY_GENERATORS, mulberry32 } from "./sampler.ts";
import {
  analyzeBitWidths,
  analyzeLoad,
  hashSamples,
  type BitWidthPoint,
  type LoadReport,
} from "./analyzer.ts";
import {
  formatBitWidthTable,
  formatLoadTable,
  summarizeDeficiencies,
} from "./report.ts";

interface CliOptions {
  samples: number;
  nodes: number;
  vnodes: number;
  seed: number;
  keys: keyof typeof KEY_GENERATORS;
  maxWidth: number;
}

function parseArgs(argv: string[]): CliOptions {
  const opts: CliOptions = {
    samples: 2_000_000,
    nodes: 50,
    vnodes: 160,
    seed: 0x5eed,
    keys: "random",
    maxWidth: 16,
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    const next = () => argv[++i];
    switch (arg) {
      case "--samples": opts.samples = Number(next()); break;
      case "--nodes": opts.nodes = Number(next()); break;
      case "--vnodes": opts.vnodes = Number(next()); break;
      case "--seed": opts.seed = Number(next()); break;
      case "--keys": opts.keys = next() as CliOptions["keys"]; break;
      case "--max-width": opts.maxWidth = Number(next()); break;
      case "--help":
        console.log(
          "Usage: node src/cli.ts [--samples N] [--nodes N] [--vnodes N]\n" +
            "  [--seed N] [--keys random|sequential|hex] [--max-width BITS]",
        );
        process.exit(0);
      default:
        throw new Error(`unknown option: ${arg}`);
    }
  }
  if (!(opts.keys in KEY_GENERATORS)) {
    throw new Error(`unknown key generator: ${opts.keys}`);
  }
  return opts;
}

function main(): void {
  const opts = parseArgs(process.argv.slice(2));
  const keyGen = KEY_GENERATORS[opts.keys];
  const nodes = Array.from({ length: opts.nodes }, (_, i) => `node-${i}`);

  console.log("=== Consistent-Hash Ring Distribution Analyzer ===");
  console.log(
    `samples=${opts.samples.toLocaleString()} nodes=${opts.nodes} ` +
      `vnodes/node=${opts.vnodes} keys=${opts.keys} seed=${opts.seed}`,
  );

  const widths: number[] = [];
  for (let b = 4; b <= opts.maxWidth; b += 2) widths.push(b);

  const loadReports: LoadReport[] = [];
  const curves: { name: string; points: BitWidthPoint[] }[] = [];

  for (const spec of HASH_REGISTRY) {
    const rand = mulberry32(opts.seed);
    const nextKey = (i: number) => keyGen(i, rand);
    const t0 = performance.now();
    const hashes = hashSamples(spec.fn, opts.samples, nextKey);
    const report = analyzeLoad({
      hashName: spec.name,
      hash: spec.fn,
      nodes,
      sampleCount: opts.samples,
      vnodesPerNode: opts.vnodes,
      nextKey,
      hashes,
    });
    const points = analyzeBitWidths(hashes, widths);
    const elapsed = ((performance.now() - t0) / 1000).toFixed(2);
    console.log(`  [${spec.name}] analyzed in ${elapsed}s`);
    loadReports.push(report);
    curves.push({ name: spec.name, points });
  }

  console.log("\n--- Node load balance on the consistent-hash ring ---");
  console.log(formatLoadTable(loadReports));

  console.log("\n--- Bit-width variance curve (chi-square uniformity vs truncated width) ---");
  for (const { name, points } of curves) {
    console.log(formatBitWidthTable(name, points));
  }

  console.log("\n--- Diagnostic conclusion ---");
  console.log(summarizeDeficiencies(curves));
}

main();
