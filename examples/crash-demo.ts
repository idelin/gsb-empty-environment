import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Database } from "../src/database.ts";
import { PAGE_HEADER_SIZE } from "../src/disk.ts";

const ACCOUNT_COUNT = 10;
const INITIAL_BALANCE = 100;
const SLOT = PAGE_HEADER_SIZE;

function writeBalance(db: Database, txnId: number, pageId: number, account: number, value: number): void {
  const buf = Buffer.alloc(4);
  buf.writeInt32LE(value);
  db.write(txnId, pageId, SLOT + account * 4, buf);
}

function readBalance(db: Database, pageId: number, account: number): number {
  return db.read(pageId, SLOT + account * 4, 4).readInt32LE(0);
}

function printBalances(db: Database, pageId: number, label: string): void {
  const balances = Array.from({ length: ACCOUNT_COUNT }, (_, i) => readBalance(db, pageId, i));
  const total = balances.reduce((a, b) => a + b, 0);
  console.log(`${label}`);
  console.log(`  balances: [${balances.join(", ")}]`);
  console.log(`  total:    ${total}`);
}

function transfer(db: Database, txnId: number, pageId: number, from: number, to: number, amount: number): void {
  writeBalance(db, txnId, pageId, from, readBalance(db, pageId, from) - amount);
  writeBalance(db, txnId, pageId, to, readBalance(db, pageId, to) + amount);
}

const dir = mkdtempSync(join(tmpdir(), "aries-demo-"));
console.log(`=== ARIES crash-recovery demo ===`);
console.log(`data dir: ${dir}\n`);

console.log(`[phase 1] boot & seed ${ACCOUNT_COUNT} accounts with ${INITIAL_BALANCE} each`);
let db = Database.open(dir);
const page = db.allocatePage();
const seed = db.begin();
for (let i = 0; i < ACCOUNT_COUNT; i++) writeBalance(db, seed, page, i, INITIAL_BALANCE);
db.commit(seed);
db.checkpoint();
printBalances(db, page, "[phase 1] seeded state (checkpointed)");

console.log(`\n[phase 2] batch of 5 transfers on disjoint accounts; commit #1/#3/#5, leave #2/#4 open`);
const transfers: Array<{ txn: number; from: number; to: number; amount: number; committed: boolean }> = [];
for (let i = 0; i < 5; i++) {
  const txn = db.begin();
  const from = i * 2;
  const to = i * 2 + 1;
  const amount = 10 * (i + 1);
  transfer(db, txn, page, from, to, amount);
  const committed = i % 2 === 0;
  if (committed) db.commit(txn);
  transfers.push({ txn, from, to, amount, committed });
  console.log(
    `  txn ${txn}: move ${amount} from acct ${from} to acct ${to} -> ${committed ? "COMMITTED" : "still open"}`,
  );
}
printBalances(db, page, "[phase 2] in-memory state right before power loss");

console.log(`\n[phase 3] *** POWER LOSS *** dirty buffer pages are gone, WAL survives`);
console.log(`  dirty pages lost: [${db.buffer.dirtyPageIds().join(", ")}]`);
db.crash();
db = undefined as unknown as Database;

console.log(`\n[phase 4] reboot: Database.open() runs ARIES recovery`);
const recovered = Database.open(dir);
const report = recovered.recoveryReport;
console.log(`  analysis: winners=[${report.analysis.winners}] losers=[${report.analysis.losers}] dpt size=${report.analysis.dirtyPages}`);
console.log(`  redo:     startLSN=${report.redo.startLSN} applied=${report.redo.applied} skipped=${report.redo.skipped}`);
console.log(`  undo:     txns=[${report.undo.txnIds}] clr written=${report.undo.clrCount}`);
printBalances(recovered, page, "[phase 4] recovered state");

const expected = Array.from({ length: ACCOUNT_COUNT }, () => INITIAL_BALANCE);
for (const t of transfers) {
  if (!t.committed) continue;
  expected[t.from] -= t.amount;
  expected[t.to] += t.amount;
}
const actual = Array.from({ length: ACCOUNT_COUNT }, (_, i) => readBalance(recovered, page, i));
const ok = actual.every((v, i) => v === expected[i]);
console.log(`\n[phase 5] verification`);
console.log(`  expected: [${expected.join(", ")}]`);
console.log(`  actual:   [${actual.join(", ")}]`);
console.log(`  committed transfers applied, uncommitted rolled back: ${ok ? "OK" : "MISMATCH"}`);
console.log(`  total preserved (no corruption): ${actual.reduce((a, b) => a + b, 0) === ACCOUNT_COUNT * INITIAL_BALANCE ? "OK" : "MISMATCH"}`);
recovered.shutdown();
if (!ok) process.exit(1);
