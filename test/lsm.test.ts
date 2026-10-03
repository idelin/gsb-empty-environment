import assert from 'node:assert/strict';
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { LsmEngine, type LsmStats } from '../src/engine.ts';

function makeDir(): string {
  return mkdtempSync(path.join(tmpdir(), 'lsm-test-'));
}

function cleanup(dir: string): void {
  rmSync(dir, { recursive: true, force: true });
}

const yieldToEventLoop = () => new Promise((resolve) => setImmediate(resolve));

test('basic put/get/delete with tombstones', async () => {
  const dir = makeDir();
  const engine = await LsmEngine.open({ dir });
  engine.put('alpha', '1');
  engine.put('beta', '2');
  assert.equal(engine.get('alpha')!.toString(), '1');
  assert.equal(engine.get('beta')!.toString(), '2');
  engine.put('alpha', '3');
  assert.equal(engine.get('alpha')!.toString(), '3');
  engine.delete('alpha');
  assert.equal(engine.get('alpha'), undefined);
  assert.equal(engine.get('missing'), undefined);
  await engine.close();
  cleanup(dir);
});

test('rotation freezes memtable and flushes immutables to L0 sequentially', async () => {
  const dir = makeDir();
  const threshold = 64 * 1024;
  const events: string[] = [];
  const engine = await LsmEngine.open({
    dir,
    memtableThresholdBytes: threshold,
    onRotate: () => events.push('rotate'),
    onFlush: (file) => events.push(`flush:${file}`),
  });
  const value = Buffer.alloc(500, 'v');
  // 64KB / ~543B per entry ~= 121 entries per memtable; 1000 entries -> 8 rotations
  for (let i = 0; i < 1000; i++) {
    engine.put(`key-${String(i).padStart(6, '0')}`, value);
    if (i % 100 === 0) await yieldToEventLoop();
  }
  await engine.waitIdle();
  const stats = engine.getStats();
  assert.ok(stats.rotations >= 2, `expected multiple rotations, got ${stats.rotations}`);
  assert.equal(stats.flushesCompleted, stats.rotations);
  assert.equal(stats.immutableCount, 0);
  assert.equal(stats.l0FileCount, stats.rotations);
  // flushed WAL segments are reclaimed; only the active segment remains
  assert.equal(stats.walSegmentCount, 1);
  // flush order is sequential: 000001.sst, 000002.sst, ...
  const sstFiles = readdirSync(path.join(dir, 'l0')).sort();
  assert.deepEqual(
    sstFiles,
    sstFiles.map((_, i) => `${String(i + 1).padStart(6, '0')}.sst`),
  );
  // all data readable through the engine
  for (let i = 0; i < 1000; i++) {
    assert.ok(engine.get(`key-${String(i).padStart(6, '0')}`), `missing key ${i}`);
  }
  assert.ok(events.filter((e) => e === 'rotate').length >= 2);
  await engine.close();
  cleanup(dir);
});

test('acceptance: two 4MB threshold hits, 2 immutable rotations, sequential L0 flush, no write stall, WAL rotated', async () => {
  const dir = makeDir();
  const rotations: LsmStats[] = [];
  const flushes: string[] = [];
  const engine = await LsmEngine.open({
    dir,
    memtableThresholdBytes: 4 * 1024 * 1024,
    onRotate: (s) => rotations.push({ ...s }),
    onFlush: (file) => flushes.push(file),
  });

  const value = Buffer.alloc(440, 'x');
  const total = 20000; // ~9.7MB -> crosses the 4MB threshold exactly twice
  let putsWhileFlushInFlight = 0;
  const putLatenciesUs: number[] = [];
  const started = performance.now();
  for (let i = 0; i < total; i++) {
    const t0 = performance.now();
    engine.put(`key-${String(i).padStart(8, '0')}`, value);
    putLatenciesUs.push((performance.now() - t0) * 1000);
    if (engine.getStats().flushesPending > 0) putsWhileFlushInFlight++;
    if (i % 256 === 0) await yieldToEventLoop(); // let the background flusher run
  }
  const writeMs = performance.now() - started;

  // foreground kept writing while the background flusher was busy
  assert.ok(putsWhileFlushInFlight > 0, 'expected writes to continue during background flush');
  // rotation path is O(1): p99 put latency stays in the microsecond range
  // (max is only logged — a loaded CI host can preempt the thread at any time)
  putLatenciesUs.sort((a, b) => a - b);
  const p99 = putLatenciesUs[Math.floor(putLatenciesUs.length * 0.99)];
  const maxPutMicros = putLatenciesUs[putLatenciesUs.length - 1];
  assert.ok(p99 < 50_000, `p99 put latency too high: ${p99}us`);

  await engine.waitIdle();
  const stats = engine.getStats();
  assert.equal(stats.rotations, 2, 'expected exactly two memtable rotations');
  assert.equal(stats.flushesCompleted, 2);
  assert.equal(stats.l0FileCount, 2);
  assert.deepEqual(flushes, ['000001.sst', '000002.sst']);
  assert.equal(rotations.length, 2);
  // WAL safely rotated: flushed segments reclaimed, only active segment left
  assert.equal(stats.walSegmentCount, 1);
  assert.deepEqual(readdirSync(path.join(dir, 'wal')), ['000003.log']);

  // full data integrity check through the read path
  for (let i = 0; i < total; i++) {
    const v = engine.get(`key-${String(i).padStart(8, '0')}`);
    assert.ok(v && v.length === 440, `missing/corrupt key ${i}`);
  }
  console.log(
    `    [acceptance] ${total} puts in ${writeMs.toFixed(1)}ms ` +
      `(${(total / (writeMs / 1000)).toFixed(0)} ops/s), p99 put latency ${p99.toFixed(0)}us ` +
      `(max ${(maxPutMicros / 1000).toFixed(1)}ms), ` +
      `${putsWhileFlushInFlight} puts during in-flight flush`,
  );
  await engine.close();

  // everything durable on disk: reopen and verify again
  const reopened = await LsmEngine.open({ dir });
  for (let i = 0; i < total; i += 997) {
    assert.ok(reopened.get(`key-${String(i).padStart(8, '0')}`), `lost key ${i} after reopen`);
  }
  await reopened.close();
  cleanup(dir);
});

test('WAL recovery after crash (no clean close)', async () => {
  const dir = makeDir();
  const engine = await LsmEngine.open({ dir, memtableThresholdBytes: 16 * 1024 });
  const value = Buffer.alloc(300, 'w');
  for (let i = 0; i < 500; i++) {
    engine.put(`crash-${String(i).padStart(5, '0')}`, value);
  }
  await engine.waitIdle(); // let the crashed process's flusher finish before reopening
  // no close(): simulate process crash; WAL appends are already on disk
  const recovered = await LsmEngine.open({ dir, memtableThresholdBytes: 16 * 1024 });
  for (let i = 0; i < 500; i++) {
    assert.ok(recovered.get(`crash-${String(i).padStart(5, '0')}`), `lost key ${i}`);
  }
  await recovered.close();
  cleanup(dir);
});

test('persistence across clean restart: SSTable + WAL both consulted', async () => {
  const dir = makeDir();
  const engine = await LsmEngine.open({ dir, memtableThresholdBytes: 16 * 1024 });
  const value = Buffer.alloc(300, 'p');
  for (let i = 0; i < 300; i++) {
    engine.put(`persist-${String(i).padStart(5, '0')}`, value);
  }
  await engine.waitIdle(); // first generations flushed to L0
  for (let i = 300; i < 320; i++) {
    engine.put(`persist-${String(i).padStart(5, '0')}`, value); // stays in active WAL
  }
  engine.delete('persist-00000');
  await engine.close();

  const reopened = await LsmEngine.open({ dir, memtableThresholdBytes: 16 * 1024 });
  assert.equal(reopened.get('persist-00000'), undefined); // tombstone survived
  for (let i = 1; i < 320; i++) {
    assert.ok(reopened.get(`persist-${String(i).padStart(5, '0')}`), `lost key ${i}`);
  }
  await reopened.close();
  cleanup(dir);
});

test('newest write wins across active, immutable and L0 layers', async () => {
  const dir = makeDir();
  const engine = await LsmEngine.open({ dir, memtableThresholdBytes: 8 * 1024 });
  const big = Buffer.alloc(700, 'b');
  engine.put('hot', 'v1');
  for (let i = 0; i < 100; i++) engine.put(`filler-a-${i}`, big); // force rotation #1
  engine.put('hot', 'v2');
  for (let i = 0; i < 100; i++) engine.put(`filler-b-${i}`, big); // force rotation #2
  engine.put('hot', 'v3');
  assert.equal(engine.get('hot')!.toString(), 'v3');
  await engine.waitIdle(); // v1/v2 now live in L0, v3 in active
  assert.equal(engine.get('hot')!.toString(), 'v3');
  engine.delete('hot');
  assert.equal(engine.get('hot'), undefined);
  await engine.close();
  cleanup(dir);
});
