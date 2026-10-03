import { test } from "node:test";
import assert from "node:assert/strict";
import { runStress } from "../src/stress.ts";

test(
  "acceptance: 1 writer + 5 readers, dense updates on 1000 keys, zero snapshot tearing",
  { timeout: 120_000 },
  async () => {
    const report = await runStress({
      keyCount: 1000,
      txCount: 120_000,
      readerCount: 5,
      baseValue: 1000,
    });

    console.log(
      `    writer: ${report.txCount} tx in ${report.writerElapsedMs.toFixed(1)}ms ` +
        `(${report.writerTxPerSec} tx/s); readers: ${report.totalScans} scans`,
    );

    assert.ok(report.totalScans > 0, "readers must have completed scans");
    assert.equal(report.totalViolations, 0, "every snapshot checksum must equal the conserved sum");
    assert.equal(report.tornSnapshots.length, 0);
    assert.equal(report.finalSum, report.expectedSum, "final state must conserve the checksum");
  },
);
