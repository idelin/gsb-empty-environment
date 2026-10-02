import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { RecordType } from "../src/logrecord.ts";
import { MiniDB } from "../src/minidb.ts";
import { BufferPool, PageStore } from "../src/pagestore.ts";
import { WalWriter, mapWal, scanWal } from "../src/wal.ts";

const yieldNow = () => new Promise((r) => setImmediate(r));

test("concurrent appends are serialized: no torn frames, monotone LSNs", async () => {
  const dir = mkdtempSync(join(tmpdir(), "aries-conc-wal-"));
  const path = join(dir, "wal.log");
  const wal = await WalWriter.create(path);
  const WORKERS = 8;
  const PER_WORKER = 50;
  await Promise.all(
    Array.from({ length: WORKERS }, async (_, worker) => {
      for (let seq = 0; seq < PER_WORKER; seq++) {
        const payload = Buffer.alloc(4);
        payload.writeUInt16LE(worker, 0);
        payload.writeUInt16LE(seq, 2);
        await wal.append({
          type: RecordType.UPDATE, lsn: 0, txnId: worker + 1, prevLSN: 0,
          pageId: 1, offset: 0, beforeImage: Buffer.alloc(0), afterImage: payload,
        });
        if (seq % 7 === 0) await yieldNow(); // force interleaving
      }
    }),
  );
  await wal.flush();
  await wal.close();

  const records = [...scanWal(mapWal(path))];
  assert.equal(records.length, WORKERS * PER_WORKER, "every frame intact");
  for (let i = 1; i < records.length; i++) {
    assert.ok(records[i].lsn > records[i - 1].lsn, "LSNs strictly increase");
  }
  const seen = new Map<number, Set<number>>();
  for (const { record } of records) {
    assert.equal(record.type, RecordType.UPDATE);
    if (record.type !== RecordType.UPDATE) continue;
    const buf = Buffer.from(record.afterImage);
    const worker = buf.readUInt16LE(0);
    const seq = buf.readUInt16LE(2);
    assert.equal(record.txnId, worker + 1, "payload not interleaved");
    if (!seen.has(worker)) seen.set(worker, new Set());
    seen.get(worker)!.add(seq);
  }
  for (let w = 0; w < WORKERS; w++) {
    assert.equal(seen.get(w)?.size, PER_WORKER, `worker ${w} complete`);
  }
});

test("concurrent transactions keep consistent prevLSN chains", async () => {
  const dir = mkdtempSync(join(tmpdir(), "aries-conc-txn-"));
  const walPath = join(dir, "wal.log");
  const dataPath = join(dir, "data.db");
  const db = await MiniDB.create(walPath, dataPath);
  const txns = [db.begin(), db.begin(), db.begin(), db.begin()];
  await Promise.all(
    txns.map(async (txn, i) => {
      for (let seq = 0; seq < 10; seq++) {
        const data = Buffer.from(`t${txn}-s${seq}!!`);
        await db.update(txn, (i % 3) + 1, seq * 8, data);
        if (seq % 3 === 0) await yieldNow();
      }
      await db.commit(txn);
    }),
  );
  await db.crash();

  // Walk each transaction's chain backwards from its COMMIT record.
  const records = [...scanWal(mapWal(walPath))];
  const byLsn = new Map(records.map((e) => [e.lsn, e.record]));
  for (const txn of txns) {
    const commit = records.find(
      (e) => e.record.type === RecordType.COMMIT && e.record.txnId === txn,
    );
    assert.ok(commit, `txn ${txn} committed`);
    const updates: number[] = [];
    let lsn = commit!.record.prevLSN;
    while (lsn !== 0) {
      const rec = byLsn.get(lsn);
      assert.ok(rec, `chain link ${lsn} exists`);
      assert.equal(rec!.txnId, txn, "chain stays within its transaction");
      if (rec!.type === RecordType.UPDATE) updates.push(lsn);
      lsn = rec!.prevLSN;
    }
    assert.equal(updates.length, 10, `txn ${txn} chain covers all its updates`);
  }
});

test("concurrent buffer pool pin/unpin stays balanced and leak-free", async () => {
  const dir = mkdtempSync(join(tmpdir(), "aries-conc-pool-"));
  const store = await PageStore.create(join(dir, "data.db"));
  const pool = new BufferPool(store);
  const PAGES = 4;
  const TASKS = 40;
  await Promise.all(
    Array.from({ length: TASKS }, async (_, i) => {
      const pageId = (i % PAGES) + 1;
      const page = await pool.pin(pageId);
      page[8] = i & 0xff;
      await yieldNow();
      pool.unpin(pageId, { dirty: true, recLSN: i + 1 });
    }),
  );
  pool.assertNoLeaks();
  const stats = pool.stats;
  assert.equal(stats.pinned, 0);
  assert.ok(stats.allocations <= PAGES, "cache reused, no duplicate frames");
  await pool.flushAll();
  pool.assertNoLeaks();
  await store.close();
  assert.ok(store.closed);
});

test("unpin of an unpinned page is detected", async () => {
  const dir = mkdtempSync(join(tmpdir(), "aries-conc-leak-"));
  const store = await PageStore.create(join(dir, "data.db"));
  const pool = new BufferPool(store);
  await pool.pin(1);
  pool.unpin(1);
  assert.throws(() => pool.unpin(1), /unpinned/);
  pool.assertNoLeaks();
  await pool.pin(2);
  assert.throws(() => pool.assertNoLeaks(), /leaked/);
  pool.unpin(2);
  pool.assertNoLeaks();
  await store.close();
});
