/**
 * End-to-end demo: build a WAL with 3 uncommitted + 2 committed transactions,
 * crash after a checkpoint, then run ARIES analysis/redo/undo and verify.
 *
 * Run: npm run demo
 */

import { RecordType, type LogRecord } from "../src/logrecord.ts";
import { recover } from "../src/recovery.ts";
import { buildCrashScenario, expectedPage } from "../src/scenario.ts";
import { mapWal, scanWal } from "../src/wal.ts";
import { PAGE_DATA_OFFSET, PageStore, getPageLSN } from "../src/pagestore.ts";

const TYPE_NAMES: Record<number, string> = {
  [RecordType.UPDATE]: "UPDATE",
  [RecordType.COMMIT]: "COMMIT",
  [RecordType.ABORT]: "ABORT",
  [RecordType.END]: "END",
  [RecordType.CLR]: "CLR",
  [RecordType.BEGIN_CHECKPOINT]: "BEGIN_CKPT",
  [RecordType.END_CHECKPOINT]: "END_CKPT",
};

function describe(r: LogRecord): string {
  const base = `txn=${r.txnId} prev=${r.prevLSN}`;
  switch (r.type) {
    case RecordType.UPDATE:
      return `${base} page=${r.pageId} off=${r.offset} after=${JSON.stringify(Buffer.from(r.afterImage).toString())}`;
    case RecordType.CLR:
      return `${base} page=${r.pageId} off=${r.offset} undoNext=${r.undoNextLSN}`;
    case RecordType.END_CHECKPOINT:
      return `begin=${r.beginLSN} txns=${r.txns.length} dirtyPages=${r.pages.length}`;
    default:
      return base;
  }
}

function printWal(label: string, walPath: string): void {
  console.log(`\n=== ${label} ===`);
  for (const { lsn, record } of scanWal(mapWal(walPath))) {
    console.log(`  LSN ${String(lsn).padStart(5)}  ${TYPE_NAMES[record.type] ?? "UNKNOWN"}  ${describe(record)}`);
  }
}

function slot(page: Buffer, index: number): string {
  const raw = page.subarray(PAGE_DATA_OFFSET + index * 8, PAGE_DATA_OFFSET + index * 8 + 8);
  return raw.every((b) => b === 0) ? "<zero>" : JSON.stringify(raw.toString());
}

const scenario = await buildCrashScenario();
console.log(`scenario dir: ${scenario.dir}`);
printWal("WAL at crash (3 uncommitted: T1/T3/T5, 2 committed: T2/T4)", scenario.walPath);

const store = await PageStore.open(scenario.dataPath);
const report = await recover(scenario.walPath, store);

console.log("\n=== Analysis ===");
console.log(`  checkpoint LSN: ${report.analysis.checkpointLSN}, redo starts at LSN ${report.analysis.redoLSN}`);
for (const [txnId, t] of report.analysis.txnTable) {
  console.log(`  TT  txn=${txnId} status=${t.status} lastLSN=${t.lastLSN}`);
}
for (const [pageId, recLSN] of report.analysis.dpt) {
  console.log(`  DPT page=${pageId} recLSN=${recLSN}`);
}

console.log("\n=== Redo (repeat history) ===");
console.log(`  redone:  [${report.redo.redone.join(", ")}]`);
console.log(`  skipped: [${report.redo.skipped.join(", ")}]`);

console.log("\n=== Undo (losers, with CLRs) ===");
console.log(`  rolled back txns: [${report.undo.undoneTxns.join(", ")}]`);
console.log(`  CLRs written at:  [${report.undo.clrs.join(", ")}]`);
console.log(`  ENDs written at:  [${report.undo.ends.join(", ")}]`);

printWal("WAL after recovery (CLRs + ENDs appended)", scenario.walPath);

console.log("\n=== Final page contents (from disk) ===");
let ok = true;
for (const pageId of [1, 2, 3]) {
  const page = await store.readPageRaw(pageId);
  const expect = expectedPage(pageId);
  const match = page.subarray(PAGE_DATA_OFFSET).equals(expect.subarray(PAGE_DATA_OFFSET));
  ok &&= match;
  console.log(
    `  page ${pageId} pageLSN=${getPageLSN(page)} slots=[${[0, 1, 2, 3].map((i) => slot(page, i)).join(", ")}] ${match ? "OK" : "MISMATCH"}`,
  );
}
await store.close();

console.log("\n=== Re-recovery must be idempotent ===");
const store2 = await PageStore.open(scenario.dataPath);
const again = await recover(scenario.walPath, store2);
console.log(`  losers after first recovery: ${again.undo.undoneTxns.length} (expect 0)`);
for (const pageId of [1, 2, 3]) {
  const page = await store2.readPageRaw(pageId);
  ok &&= page.subarray(PAGE_DATA_OFFSET).equals(expectedPage(pageId).subarray(PAGE_DATA_OFFSET));
}
await store2.close();

console.log(ok ? "\nDEMO OK: committed data persisted, uncommitted data rolled back." : "\nDEMO FAILED");
process.exit(ok ? 0 : 1);
