import { runStress } from "./stress.ts";

const report = await runStress({
  keyCount: 1000,
  txCount: 300_000,
  readerCount: 5,
  baseValue: 1000,
});

console.log("=== Lock-free MVCC stress report ===");
console.log(`keys:                ${report.keyCount}`);
console.log(`writer transactions: ${report.txCount} (2 keys each, sum-conserving transfers)`);
console.log(`reader threads:      ${report.readerCount} (full-table checksum scans)`);
console.log(`expected sum:        ${report.expectedSum}`);
console.log(`final sum:           ${report.finalSum}`);
console.log(`writer elapsed:      ${report.writerElapsedMs.toFixed(1)} ms (${report.writerTxPerSec} tx/s, never blocked)`);
for (const [i, r] of report.readers.entries()) {
  console.log(`reader ${i}:            ${r.scans} scans, ${r.violations} violations`);
}
console.log(`total scans:         ${report.totalScans}`);
console.log(`torn snapshots:      ${report.totalViolations}`);

if (report.totalViolations !== 0 || report.finalSum !== report.expectedSum) {
  console.error("FAILED: snapshot tearing detected");
  process.exit(1);
}
console.log("PASSED: zero tearing, checksum conserved across every snapshot");
