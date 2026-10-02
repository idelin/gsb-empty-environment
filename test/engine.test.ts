import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { LsmEngine, DEFAULT_MEMTABLE_THRESHOLD_BYTES } from '../src/engine.ts';

async function withDir(fn: (dir: string) => Promise<void>): Promise<void> {
  const dir = await mkdtemp(path.join(tmpdir(), 'lsm-test-'));
  try {
    await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

const valueFor = (i: number): Buffer => Buffer.alloc(1024, i % 251);

test('acceptance: 4MB threshold hit twice -> 2 immutables flushed sequentially to L0, WAL rotated, no write stall', async () => {
  await withDir(async (dir) => {
    const engine = await LsmEngine.open(dir); // default 4 MB threshold
    assert.equal(DEFAULT_MEMTABLE_THRESHOLD_BYTES, 4 * 1024 * 1024);

    const rotations: number[] = [];
    const flushes: number[] = [];
    engine.on('rotate', (e) => rotations.push(e.tableId));
    engine.on('flush:end', (e) => flushes.push(e.tableId));

    let maxPutLatencyMs = 0;
    let i = 0;
    // Keep writing at full speed until the threshold has been crossed twice.
    while (rotations.length < 2) {
      const start = performance.now();
      await engine.put(`key-${String(i).padStart(8, '0')}`, valueFor(i));
      maxPutLatencyMs = Math.max(maxPutLatencyMs, performance.now() - start);
      i++;
    }
    // Foreground keeps writing while the background flusher drains the queue.
    for (let extra = 0; extra < 200; extra++, i++) {
      await engine.put(`key-${String(i).padStart(8, '0')}`, valueFor(i));
    }
    const totalWritten = i;
    await engine.drain();

    // Two rotations -> two immutable MemTables -> two L0 SSTables, FIFO order.
    assert.equal(rotations.length, 2);
    assert.deepEqual(flushes, rotations);
    const files = await readdir(dir);
    const ssts = files.filter((f) => f.endsWith('.sst')).sort();
    const wals = files.filter((f) => f.endsWith('.log')).sort();
    assert.equal(ssts.length, 2);
    assert.deepEqual(ssts, ['l0-000000.sst', 'l0-000001.sst']);
    // WAL safely rotated: only the active segment remains on disk.
    assert.deepEqual(wals, ['wal-000002.log']);

    // No foreground stall: no single put waited on a flush.
    assert.ok(maxPutLatencyMs < 1000, `max put latency ${maxPutLatencyMs}ms`);

    // Full data integrity check across memtable + L0.
    for (let k = 0; k < totalWritten; k++) {
      const got = engine.get(`key-${String(k).padStart(8, '0')}`);
      assert.ok(got, `missing key-${k}`);
      assert.ok(got.equals(valueFor(k)), `corrupt value for key-${k}`);
    }
    await engine.close();
  });
});

test('foreground writes never block on a slow in-progress flush', async () => {
  await withDir(async (dir) => {
    let releaseFlush!: () => void;
    const gate = new Promise<void>((resolve) => (releaseFlush = resolve));
    let flushStarted!: () => void;
    const flushing = new Promise<void>((resolve) => (flushStarted = resolve));

    const engine = await LsmEngine.open(dir, {
      memtableThresholdBytes: 64 * 1024,
      flushHook: async () => {
        flushStarted();
        await gate; // hold the flusher hostage
      },
    });

    // Fill past the threshold to trigger rotation + flush.
    for (let i = 0; i < 100; i++) await engine.put(`hot-${i}`, Buffer.alloc(1024, 1));
    await flushing; // flusher is now stuck inside the gate

    // Foreground writes proceed while the flush is blocked.
    for (let i = 100; i < 200; i++) await engine.put(`hot-${i}`, Buffer.alloc(1024, 2));
    assert.equal(engine.stats().flushInProgress, true);

    releaseFlush();
    await engine.drain();
    for (let i = 0; i < 200; i++) {
      assert.ok(engine.get(`hot-${i}`)!.equals(Buffer.alloc(1024, i < 100 ? 1 : 2)));
    }
    await engine.close();
  });
});

test('flusher is single-threaded and strictly sequential (FIFO)', async () => {
  await withDir(async (dir) => {
    let inFlight = 0;
    let maxInFlight = 0;
    const order: number[] = [];
    const engine = await LsmEngine.open(dir, {
      memtableThresholdBytes: 16 * 1024,
      flushHook: async (table) => {
        inFlight++;
        maxInFlight = Math.max(maxInFlight, inFlight);
        order.push(table.id);
        await new Promise((r) => setTimeout(r, 5));
        inFlight--;
      },
    });
    // Force several rotations so multiple immutables queue up.
    for (let i = 0; i < 400; i++) await engine.put(`seq-${i}`, Buffer.alloc(512, 7));
    await engine.drain();

    assert.equal(maxInFlight, 1, 'flushes must never overlap');
    assert.deepEqual(order, [...order].sort((a, b) => a - b), 'FIFO flush order');
    assert.ok(order.length >= 2);
    await engine.close();
  });
});

test('crash-like shutdown: sealed WAL is replayed on reopen', async () => {
  await withDir(async (dir) => {
    let engine = await LsmEngine.open(dir, { memtableThresholdBytes: 1 << 20 });
    for (let i = 0; i < 50; i++) await engine.put(`live-${i}`, `value-${i}`);
    await engine.close({ flush: false }); // leave WAL on disk

    engine = await LsmEngine.open(dir, { memtableThresholdBytes: 1 << 20 });
    for (let i = 0; i < 50; i++) {
      assert.equal(engine.get(`live-${i}`)!.toString(), `value-${i}`);
    }
    await engine.close();
    // Clean close flushed the replayed table into L0.
    const ssts = (await readdir(dir)).filter((f) => f.endsWith('.sst'));
    assert.equal(ssts.length, 1);
  });
});

test('deletes propagate through flush as tombstones', async () => {
  await withDir(async (dir) => {
    const engine = await LsmEngine.open(dir, { memtableThresholdBytes: 8 * 1024 });
    for (let i = 0; i < 50; i++) await engine.put(`d-${i}`, Buffer.alloc(256, 3));
    await engine.delete('d-10');
    await engine.delete('d-20');
    await engine.close(); // clean close flushes everything

    const reopened = await LsmEngine.open(dir, { memtableThresholdBytes: 8 * 1024 });
    assert.equal(reopened.get('d-10'), undefined);
    assert.equal(reopened.get('d-20'), undefined);
    assert.ok(reopened.get('d-30')!.equals(Buffer.alloc(256, 3)));
    await reopened.close();
  });
});
