/**
 * Runnable end-to-end demo: builds a WAL with 5 transactions (2 committed,
 * 3 in flight), checkpoints, crashes, then runs ARIES three-phase recovery.
 *
 *   npm run demo
 */

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { Database } from "./src/database";
import { recover } from "./src/recovery";
import { Wal } from "./src/wal";
import { PageStore, PAGE_HEADER_SIZE } from "./src/storage";
import { RecordType, recordTypeName } from "./src/logrecord";

const OFF_A = PAGE_HEADER_SIZE;
const OFF_B = PAGE_HEADER_SIZE + 64;

function showPage(dataPath: string, pageId: number, offset: number, len: number): string {
  const store = new PageStore(dataPath);
  const page = store.readPage(pageId);
  store.close();
  const slice = page.subarray(offset, offset + len);
  return slice.every((b) => b === 0) ? "<zeros>" : JSON.stringify(slice.toString("utf8"));
}

function main(): void {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "aries-demo-"));
  const walPath = path.join(dir, "wal.log");
  const dataPath = path.join(dir, "data.db");
  console.log(`== ARIES crash-recovery demo ==\nworkdir: ${dir}\n`);

  // ---------- normal processing ----------
  const db = new Database(walPath, dataPath);
  const t1 = db.begin();
  const t2 = db.begin();
  db.update(t1, 1, OFF_A, Buffer.from("AAAA"));
  db.update(t2, 2, OFF_A, Buffer.from("BBBB"));
  db.commit(t1);
  db.endTxn(t1);
  console.log(`T${t1} committed+ended (page1[${OFF_A}] = "AAAA")`);

  const ckptLSN = db.checkpoint();
  console.log(`fuzzy CHECKPOINT @ LSN ${ckptLSN}`);

  const t3 = db.begin();
  const t4 = db.begin();
  const t5 = db.begin();
  db.update(t2, 1, OFF_B, Buffer.from("CC"));
  db.update(t2, 5, OFF_A, Buffer.from("DDDD"));
  db.commit(t2);
  console.log(`T${t2} committed, END not yet written (page1[${OFF_B}]="CC", page5="DDDD")`);
  db.update(t3, 1, OFF_A, Buffer.from("XXXX"));
  db.update(t3, 3, OFF_A, Buffer.from("T3T3"));
  db.update(t4, 2, OFF_A, Buffer.from("YYYY"));
  db.update(t5, 4, OFF_A, Buffer.from("ZZZZ"));
  console.log(`T${t3}, T${t4}, T${t5} still in flight (uncommitted writes)`);

  db.stealPage(1); // uncommitted "XXXX" reaches disk
  db.stealPage(2); // uncommitted "YYYY" reaches disk
  console.log("steal-evicted pages 1 and 2 (uncommitted data now on disk!)");

  console.log(`\nbefore crash: page1[${OFF_A}]=${showPage(dataPath, 1, OFF_A, 4)} ` +
    `page2[${OFF_A}]=${showPage(dataPath, 2, OFF_A, 4)} page5=${showPage(dataPath, 5, OFF_A, 4)}`);
  db.crash();
  console.log("*** CRASH *** buffer pool lost, WAL + disk pages survive\n");

  // ---------- recovery ----------
  const report = recover(walPath, dataPath);
  console.log(`analysis: checkpoint@${report.analysis.lastCheckpointLSN}, ` +
    `redoLSN=${report.analysis.redoLSN}, DPT pages=` +
    `[${[...report.analysis.dirtyPages.keys()].sort((a, b) => a - b).join(",")}]`);
  console.log(`redo:     re-applied ${report.redo.appliedLSNs.length} updates ` +
    `(LSNs ${report.redo.appliedLSNs.join(", ")}), skipped ${report.redo.skipped}`);
  console.log(`undo:     wrote ${report.undo.clrLSNs.length} CLRs, ended txns ` +
    `[${report.undo.endedTxns.join(", ")}]`);

  console.log(`\nafter recovery:`);
  console.log(`  page1[${OFF_A}] = ${showPage(dataPath, 1, OFF_A, 4)}  (T1 committed, T3 rolled back)`);
  console.log(`  page1[${OFF_B}] = ${showPage(dataPath, 1, OFF_B, 2)}  (T2 committed)`);
  console.log(`  page2[${OFF_A}] = ${showPage(dataPath, 2, OFF_A, 4)}  (T2 committed, T4 rolled back)`);
  console.log(`  page3[${OFF_A}] = ${showPage(dataPath, 3, OFF_A, 4)}  (T3 loser)`);
  console.log(`  page4[${OFF_A}] = ${showPage(dataPath, 4, OFF_A, 4)}  (T5 loser)`);
  console.log(`  page5[${OFF_A}] = ${showPage(dataPath, 5, OFF_A, 4)}  (T2 committed, redone)`);

  // ---------- WAL dump ----------
  console.log(`\nfinal WAL (${fs.readFileSync(walPath).length} bytes):`);
  const wal = Wal.open(walPath);
  for (const frame of wal.scan()) {
    const r = frame.record;
    const extra =
      r.type === RecordType.Update || r.type === RecordType.Clr
        ? ` page=${r.pageId} off=${r.offset}` +
          (r.type === RecordType.Clr ? ` undoNext=${r.undoNextLSN}` : "")
        : "";
    console.log(`  LSN ${String(frame.lsn).padStart(4)}  ${recordTypeName(r).padEnd(10)} txn=${r.txnId}${extra}`);
  }
  wal.close();
  console.log(`\nresources closed: ${report.resourcesClosed}, ATT empty: ${report.analysis.txns.size === 0}`);
  fs.rmSync(dir, { recursive: true, force: true });
}

main();
