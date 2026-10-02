import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { Database } from "../src/database.ts";
import { PAGE_HEADER_SIZE } from "../src/disk.ts";

const SLOT = PAGE_HEADER_SIZE;

function tmpdirPath(): string {
  return mkdtempSync(join(tmpdir(), "aries-test-"));
}

function writeInt(db: Database, txnId: number, pageId: number, slot: number, value: number): void {
  const buf = Buffer.alloc(4);
  buf.writeInt32LE(value);
  db.write(txnId, pageId, SLOT + slot * 4, buf);
}

function readInt(db: Database, pageId: number, slot: number): number {
  return db.read(pageId, SLOT + slot * 4, 4).readInt32LE(0);
}

test("committed data survives a clean shutdown", () => {
  const dir = tmpdirPath();
  const db = Database.open(dir);
  const page = db.allocatePage();
  const txn = db.begin();
  writeInt(db, txn, page, 0, 42);
  db.commit(txn);
  db.shutdown();

  const reopened = Database.open(dir);
  assert.equal(readInt(reopened, page, 0), 42);
  reopened.shutdown();
});

test("redo: committed but unflushed dirty pages survive a crash", () => {
  const dir = tmpdirPath();
  const db = Database.open(dir);
  const page = db.allocatePage();
  const txn = db.begin();
  writeInt(db, txn, page, 0, 777);
  db.commit(txn);
  assert.deepEqual(db.buffer.dirtyPageIds(), [page]);
  db.crash();

  const recovered = Database.open(dir);
  assert.equal(recovered.recoveryReport.redo.applied, 1);
  assert.equal(readInt(recovered, page, 0), 777);
  recovered.shutdown();
});

test("undo: uncommitted transaction is rolled back after a crash", () => {
  const dir = tmpdirPath();
  const db = Database.open(dir);
  const page = db.allocatePage();

  const setup = db.begin();
  writeInt(db, setup, page, 0, 100);
  db.commit(setup);
  db.checkpoint();

  const loser = db.begin();
  writeInt(db, loser, page, 0, 999);
  db.crash();

  const recovered = Database.open(dir);
  assert.deepEqual(recovered.recoveryReport.analysis.losers, [loser]);
  assert.equal(recovered.recoveryReport.undo.clrCount, 1);
  assert.equal(readInt(recovered, page, 0), 100);
  recovered.shutdown();
});

test("mid-batch power loss: committed kept, uncommitted reverted, no corruption", () => {
  const dir = tmpdirPath();
  const db = Database.open(dir);
  const page = db.allocatePage();

  const init = db.begin();
  for (let slot = 0; slot < 10; slot++) writeInt(db, init, page, slot, 0);
  db.commit(init);
  db.checkpoint();

  const committed: number[] = [];
  const lost: number[] = [];
  for (let i = 0; i < 10; i++) {
    const txn = db.begin();
    writeInt(db, txn, page, i, (i + 1) * 111);
    if (i % 2 === 0) {
      db.commit(txn);
      committed.push(i);
    } else {
      lost.push(txn);
    }
  }
  db.buffer.flushPage(page);
  const tail = db.begin();
  writeInt(db, tail, page, 9, 5555);
  db.crash();

  const recovered = Database.open(dir);
  assert.deepEqual(recovered.recoveryReport.analysis.losers.sort(), [...lost, tail].sort());
  for (let slot = 0; slot < 10; slot++) {
    const expected = slot % 2 === 0 ? (slot + 1) * 111 : 0;
    assert.equal(readInt(recovered, page, slot), expected, `slot ${slot}`);
  }
  recovered.shutdown();
});

test("recovery is idempotent: recovering twice yields identical bytes", () => {
  const dir = tmpdirPath();
  const db = Database.open(dir);
  const page = db.allocatePage();
  const t1 = db.begin();
  writeInt(db, t1, page, 0, 1);
  db.commit(t1);
  const t2 = db.begin();
  writeInt(db, t2, page, 1, 2);
  db.crash();

  const first = Database.open(dir);
  const snapshot1 = first.read(page, 0, 64);
  first.shutdown();

  const second = Database.open(dir);
  const snapshot2 = second.read(page, 0, 64);
  assert.deepEqual(snapshot2, snapshot1);
  assert.equal(second.recoveryReport.undo.clrCount, 0);
  second.shutdown();
});

test("in-memory abort rolls back a running transaction with CLRs", () => {
  const dir = tmpdirPath();
  const db = Database.open(dir);
  const page = db.allocatePage();
  const t1 = db.begin();
  writeInt(db, t1, page, 0, 10);
  db.commit(t1);

  const t2 = db.begin();
  writeInt(db, t2, page, 0, 20);
  writeInt(db, t2, page, 1, 30);
  db.abort(t2);

  assert.equal(readInt(db, page, 0), 10);
  assert.equal(readInt(db, page, 1), 0);
  db.crash();

  const recovered = Database.open(dir);
  assert.equal(readInt(recovered, page, 0), 10);
  assert.equal(readInt(recovered, page, 1), 0);
  recovered.shutdown();
});

test("checkpoint with in-flight transactions recovers correctly", () => {
  const dir = tmpdirPath();
  const db = Database.open(dir);
  const page = db.allocatePage();

  const longLived = db.begin();
  writeInt(db, longLived, page, 0, 5);
  db.checkpoint();

  const t2 = db.begin();
  writeInt(db, t2, page, 1, 6);
  db.commit(t2);
  writeInt(db, longLived, page, 0, 7);
  db.crash();

  const recovered = Database.open(dir);
  assert.equal(readInt(recovered, page, 0), 0);
  assert.equal(readInt(recovered, page, 1), 6);
  recovered.shutdown();
});

test("transaction ids do not collide after recovery", () => {
  const dir = tmpdirPath();
  const db = Database.open(dir);
  const page = db.allocatePage();
  const t1 = db.begin();
  writeInt(db, t1, page, 0, 1);
  db.commit(t1);
  db.crash();

  const recovered = Database.open(dir);
  const t2 = recovered.begin();
  assert.ok(t2 > t1);
  writeInt(recovered, t2, page, 0, 2);
  recovered.commit(t2);
  assert.equal(readInt(recovered, page, 0), 2);
  recovered.shutdown();
});
