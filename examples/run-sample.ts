import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { exportJSON, exportPrometheus } from "../src/metrics/exporter.ts";
import { MetricsRegistry } from "../src/metrics/registry.ts";
import { RecoveryEngine } from "../src/recovery/aries.ts";
import { BufferPool } from "../src/storage/bufferPool.ts";
import { DiskManager } from "../src/storage/disk.ts";
import { buildCrashScenario, EXPECTED_DATA, PAGE_IDS } from "./scenario.ts";

const dir = mkdtempSync(join(tmpdir(), "aries-sample-"));
console.log(`== Building crash scenario in ${dir} ==`);
const site = buildCrashScenario(dir);
console.log(`Crashed after LSN ${site.lastLSN}: T1,T2 committed; T3,T4,T5 uncommitted\n`);

console.log("== Running ARIES recovery ==");
const metrics = new MetricsRegistry();
const disk = new DiskManager(site.diskPath, 4096, 16, metrics);
const bufferPool = new BufferPool(disk, metrics);
const engine = new RecoveryEngine(site.walPath, disk, bufferPool, metrics);
const report = engine.recover();

console.log(`losers (rolled back): [${report.losers.join(", ")}]`);
console.log(`redo: startLSN=${report.redoStartLSN} applied=${report.redoApplied} skipped=${report.redoSkipped}`);
console.log(`undo: CLRs written=${report.undoCLRs}\n`);

console.log("== Final committed page contents ==");
for (const [name, pageId] of Object.entries(PAGE_IDS)) {
  const page = disk.readPage(pageId);
  const expected = EXPECTED_DATA[pageId];
  const ok = page.data === expected ? "OK" : `MISMATCH (expected ${expected})`;
  console.log(`  ${name} (page ${pageId}): "${page.data}"  ${ok}`);
}
disk.close();

console.log("\n== Metrics (JSON export, truncated) ==");
const bundle = exportJSON(metrics.snapshots());
console.log("derived:", JSON.stringify(bundle.derived, null, 2));
for (const m of bundle.metrics) {
  if (m.kind === "histogram") {
    console.log(
      `  ${m.name}${JSON.stringify(m.labels)} count=${m.count} p50=${m.percentiles.p50?.toFixed(3)}ms p99=${m.percentiles.p99?.toFixed(3)}ms`,
    );
  } else {
    console.log(`  ${m.name}${JSON.stringify(m.labels)} = ${m.value}`);
  }
}

console.log("\n== Prometheus exposition (first 12 lines) ==");
console.log(exportPrometheus(metrics.snapshots()).split("\n").slice(0, 12).join("\n"));
