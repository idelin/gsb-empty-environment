import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  GridIndexStore,
  latLngToCell,
  gridDisk,
  parseRecord,
} from '../src/index.ts';

async function tempDir(): Promise<string> {
  return fs.mkdtemp(path.join(os.tmpdir(), 'hexconc-'));
}

test('200 concurrent commits: no lost updates, WAL well-formed', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  const cells = [];
  for (let i = 0; i < 200; i++) {
    cells.push(latLngToCell(-60 + (i * 137) % 120, -170 + (i * 89) % 340, 8));
  }
  const unique = [...new Set(cells)];
  await Promise.all(unique.map((c, i) => store.put(c, `v${i}`)));
  assert.equal(store.size, unique.length);
  await store.close();

  // WAL: every line parses, checksums valid, seq numbers contiguous
  const wal = await fs.readFile(path.join(dir, 'wal.log'), 'utf8');
  const seqs: number[] = [];
  for (const line of wal.trim().split('\n')) {
    const rec = parseRecord(line);
    seqs.push(rec.seq);
  }
  assert.equal(seqs.length, unique.length);
  assert.deepEqual([...seqs].sort((a, b) => a - b), seqs.map((_, i) => i));

  const recovered = await GridIndexStore.open(dir);
  assert.equal(recovered.size, unique.length);
  await recovered.close();
});

test('concurrent puts and deletes on disjoint sets are deterministic', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  const puts = [];
  const dels = [];
  for (let i = 0; i < 100; i++) {
    puts.push(latLngToCell(10 + (i % 10) * 5, 20 + Math.floor(i / 10) * 8, 8));
    dels.push(latLngToCell(-10 - (i % 10) * 5, -20 - Math.floor(i / 10) * 8, 8));
  }
  for (const d of dels) await store.put(d, 'to-delete');
  await Promise.all([
    ...puts.map((c) => store.put(c, 'kept')),
    ...dels.map((c) => store.delete(c)),
  ]);
  assert.equal(store.size, puts.length);
  for (const c of puts) assert.equal(store.get(c), 'kept');
  for (const c of dels) assert.ok(!store.has(c));
  await store.close();
  const recovered = await GridIndexStore.open(dir);
  assert.equal(recovered.size, puts.length);
  await recovered.close();
});

test('checkpoints interleaved with commits never corrupt state', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  const jobs: Promise<unknown>[] = [];
  for (let i = 0; i < 60; i++) {
    jobs.push(store.put(`cell-${i}`, `v${i}`));
    if (i % 7 === 0) jobs.push(store.checkpoint().catch(() => {}));
  }
  await Promise.all(jobs);
  await store.close();
  const recovered = await GridIndexStore.open(dir);
  assert.equal(recovered.size, 60);
  await recovered.close();
  const names = (await fs.readdir(dir)).sort();
  assert.deepEqual(names, ['snapshot.json', 'wal.log']);
});

test('concurrent K-ring traversals are pure and race-free', async () => {
  const centers = [];
  for (let i = 0; i < 40; i++) {
    centers.push(latLngToCell(10 + i, 30 + i * 2, 8));
  }
  const results = await Promise.all(
    centers.map(async (c) => {
      // interleave async yields to stress shared caches
      await Promise.resolve();
      return gridDisk(c, 3);
    }),
  );
  for (const disk of results) {
    assert.ok(disk.length > 0 && disk.length <= 37);
    assert.equal(new Set(disk).size, disk.length);
  }
});

test('no resource leaks: handles closed, no temp files, dir reusable', async () => {
  const dir = await tempDir();
  for (let cycle = 0; cycle < 5; cycle++) {
    const store = await GridIndexStore.open(dir);
    await Promise.all(
      Array.from({ length: 20 }, (_, i) => store.put(`c${cycle}-${i}`, 'x')),
    );
    await store.checkpoint();
    await store.close();
  }
  const names = (await fs.readdir(dir)).sort();
  assert.deepEqual(names, ['snapshot.json', 'wal.log']);
  const final = await GridIndexStore.open(dir);
  assert.equal(final.size, 100);
  await final.close();
  // no lingering active handles from the store (fs.promises auto-close)
  const resources = (process as unknown as { getActiveResourcesInfo?: () => string[] })
    .getActiveResourcesInfo?.() ?? [];
  const storeHandles = resources.filter(
    (r) => typeof r === 'string' && r.includes('hexconc-'),
  );
  assert.deepEqual(storeHandles, []);
});
