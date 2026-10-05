import { DEFAULT_SIM_CONFIG, runSimulation, type SimConfig } from "./simulation.ts";
import { auditLedgers, formatReport } from "./audit.ts";

function parseArgs(argv: string[]): { command: string; flags: Map<string, string> } {
  const [command = "replay", ...rest] = argv;
  const flags = new Map<string, string>();
  for (let i = 0; i < rest.length; i++) {
    if (rest[i].startsWith("--")) {
      const key = rest[i].slice(2);
      const value = rest[i + 1] && !rest[i + 1].startsWith("--") ? rest[++i] : "true";
      flags.set(key, value);
    }
  }
  return { command, flags };
}

function parsePartitions(raw: string | undefined): Array<{ from: number; to: number }> {
  if (!raw) return [];
  return raw.split(",").map((range) => {
    const [from, to] = range.split("-").map(Number);
    return { from, to };
  });
}

function parseRestarts(raw: string | undefined): Partial<Record<string, number>> {
  if (!raw) return {};
  const out: Record<string, number> = {};
  for (const pair of raw.split(",")) {
    const [node, tick] = pair.split("@");
    out[node] = Number(tick);
  }
  return out;
}

const { command, flags } = parseArgs(process.argv.slice(2));

if (command === "replay") {
  const config: SimConfig = {
    ...DEFAULT_SIM_CONFIG,
    seed: Number(flags.get("seed") ?? DEFAULT_SIM_CONFIG.seed),
    ticks: Number(flags.get("ticks") ?? DEFAULT_SIM_CONFIG.ticks),
    windowLength: Number(flags.get("window") ?? DEFAULT_SIM_CONFIG.windowLength),
    windowQuota: Number(flags.get("quota") ?? DEFAULT_SIM_CONFIG.windowQuota),
    lossRate: Number(flags.get("loss") ?? DEFAULT_SIM_CONFIG.lossRate),
    dupRate: Number(flags.get("dup") ?? DEFAULT_SIM_CONFIG.dupRate),
    partitions: parsePartitions(flags.get("partition") ?? "120-139"),
    restartAt: parseRestarts(flags.get("restart") ?? "A@170"),
    outDir: flags.get("out") ?? DEFAULT_SIM_CONFIG.outDir,
  };
  const result = runSimulation(config);
  console.log(
    `simulation done: dir=${result.dir} dropped=${result.busStats.dropped} duplicated=${result.busStats.duplicated} inFlight=${result.busStats.inFlight}`,
  );
  const report = auditLedgers({
    dir: result.dir,
    nodeIds: config.nodeIds,
    windowQuota: config.windowQuota,
    windowLength: config.windowLength,
  });
  console.log(formatReport(report));
  process.exit(report.violations.length === 0 ? 0 : 1);
} else if (command === "audit") {
  const dir = flags.get("dir");
  if (!dir) {
    console.error("usage: node src/cli.ts audit --dir <ledger-dir> [--quota N] [--window N] [--nodes A,B]");
    process.exit(2);
  }
  const report = auditLedgers({
    dir,
    nodeIds: (flags.get("nodes") ?? "A,B").split(","),
    windowQuota: Number(flags.get("quota") ?? DEFAULT_SIM_CONFIG.windowQuota),
    windowLength: Number(flags.get("window") ?? DEFAULT_SIM_CONFIG.windowLength),
  });
  console.log(formatReport(report));
  process.exit(report.violations.length === 0 ? 0 : 1);
} else {
  console.error("usage: node src/cli.ts <replay|audit> [flags]");
  console.error("  replay --seed 1 --ticks 400 --window 50 --quota 120 --loss 0.1 --dup 0.05 \\");
  console.error("         --partition 120-139 --restart A@170 --out data/run");
  console.error("  audit  --dir data/run [--nodes A,B] [--quota 120] [--window 50]");
  process.exit(2);
}
