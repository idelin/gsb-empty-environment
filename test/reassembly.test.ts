import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash, randomBytes } from 'node:crypto';
import { promises as fs } from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { ReassemblyStore } from '../src/reassembly.ts';

const sha256 = (buf: Buffer | Uint8Array): string => createHash('sha256').update(buf).digest('hex');

async function tempDir(): Promise<string> {
  return fs.mkdtemp(path.join(os.tmpdir(), 'reassembly-'));
}

/** Deterministic pseudo-random content so segments can overlap meaningfully. */
function makeContent(size: number, seed = 1): Buffer {
  const out = Buffer.alloc(size);
  for (let i = 0; i < size; i++) out[i] = (seed * 31 + i * 7 + (i >> 3)) & 0xff;
  return out;
}

test('empty file publishes immediately when digest matches', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const state = await store.declareFile('empty.bin', 1, 0, sha256(Buffer.alloc(0)));
  assert.equal(state, 'published');
  const published = await fs.readFile(store.publishedPath('empty.bin'));
  assert.equal(published.length, 0);
  assert.equal(store.getVersionInfo('empty.bin', 1)?.state, 'published');
});

test('empty file with wrong digest fails instead of publishing', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const state = await store.declareFile('empty.bin', 1, 0, sha256(Buffer.from('nope')));
  assert.equal(state, 'failed');
  await assert.rejects(fs.access(store.publishedPath('empty.bin')));
});

test('out-of-bounds segments are rejected and do not corrupt state', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const content = makeContent(100);
  await store.declareFile('f', 1, content.length, sha256(content));

  assert.equal(await store.addSegment('f', 1, 95, Buffer.alloc(10)), 'rejected-out-of-bounds');
  assert.equal(await store.addSegment('f', 1, 100, Buffer.from('x')), 'rejected-out-of-bounds');
  assert.equal(await store.addSegment('f', 1, -1, Buffer.from('x')), 'rejected-out-of-bounds');
  assert.equal(await store.addSegment('f', 1, 0, Buffer.alloc(200)), 'rejected-out-of-bounds');

  const info = store.getVersionInfo('f', 1);
  assert.equal(info?.state, 'receiving');
  assert.equal(info?.receivedBytes, 0);

  // Segment ending exactly at size is in bounds and completes the file.
  assert.equal(await store.addSegment('f', 1, 0, content), 'published');
});

test('out-of-order, overlapping and duplicate segments reassemble correctly', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const content = randomBytes(4096);
  await store.declareFile('f', 1, content.length, sha256(content));

  // Out-of-order, mutually overlapping slices; only the last one completes coverage.
  assert.equal(await store.addSegment('f', 1, 2000, content.subarray(2000, 3500)), 'stored');
  assert.equal(await store.addSegment('f', 1, 0, content.subarray(0, 1200)), 'stored');
  // Exact re-delivery of an earlier slice is a harmless duplicate.
  assert.equal(await store.addSegment('f', 1, 0, content.subarray(0, 1200)), 'duplicate');
  assert.equal(await store.addSegment('f', 1, 3000, content.subarray(3000, 4096)), 'stored');
  // Overlaps both ends and fills the [1200, 2000) gap.
  assert.equal(await store.addSegment('f', 1, 1000, content.subarray(1000, 2100)), 'published');

  const published = await fs.readFile(store.publishedPath('f'));
  assert.deepEqual(published, content);
});

test('conflicting overlap quarantines the version and rejects further segments', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const content = makeContent(64);
  await store.declareFile('f', 1, content.length, sha256(content));

  assert.equal(await store.addSegment('f', 1, 0, content.subarray(0, 32)), 'stored');
  const tampered = Buffer.from(content.subarray(16, 48));
  tampered[0] = tampered[0]! ^ 0xff; // differs inside the already-received [0,32) range
  assert.equal(await store.addSegment('f', 1, 16, tampered), 'quarantined-conflict');

  assert.equal(store.getVersionInfo('f', 1)?.state, 'quarantined');
  assert.equal(await store.addSegment('f', 1, 32, content.subarray(32)), 'rejected-not-receiving');
  await assert.rejects(fs.access(store.publishedPath('f')));
});

test('identical retransmission of overlapping bytes is not a conflict', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const content = makeContent(64);
  await store.declareFile('f', 1, content.length, sha256(content));
  assert.equal(await store.addSegment('f', 1, 0, content.subarray(0, 40)), 'stored');
  assert.equal(await store.addSegment('f', 1, 20, content.subarray(20, 64)), 'published');
});

test('full coverage with wrong digest fails and is not published', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const content = makeContent(128);
  await store.declareFile('f', 1, content.length, sha256(makeContent(128, 99)));
  assert.equal(await store.addSegment('f', 1, 0, content), 'failed-digest');
  assert.equal(store.getVersionInfo('f', 1)?.state, 'failed');
  await assert.rejects(fs.access(store.publishedPath('f')));
});

test('interleaved versions are tracked independently', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const v1 = makeContent(256, 1);
  const v2 = makeContent(256, 2);
  await store.declareFile('f', 1, v1.length, sha256(v1));
  await store.declareFile('f', 2, v2.length, sha256(v2));

  assert.equal(await store.addSegment('f', 1, 0, v1.subarray(0, 128)), 'stored');
  assert.equal(await store.addSegment('f', 2, 128, v2.subarray(128)), 'stored');
  assert.equal(await store.addSegment('f', 2, 0, v2.subarray(0, 128)), 'published');
  assert.equal(await store.addSegment('f', 1, 128, v1.subarray(128)), 'published');

  // v2 finished first, then v1 — last publish wins for the published path.
  assert.deepEqual(await fs.readFile(store.publishedPath('f')), v1);
  assert.equal(store.getVersionInfo('f', 1)?.state, 'published');
  assert.equal(store.getVersionInfo('f', 2)?.state, 'published');
});

test('quarantining one version does not affect another version of the same file', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const v1 = makeContent(64, 1);
  const v2 = makeContent(64, 2);
  await store.declareFile('f', 1, v1.length, sha256(v1));
  await store.declareFile('f', 2, v2.length, sha256(v2));

  await store.addSegment('f', 1, 0, v1.subarray(0, 32));
  const bad = Buffer.from(v1.subarray(16, 48));
  bad[0] = bad[0]! ^ 0xff;
  assert.equal(await store.addSegment('f', 1, 16, bad), 'quarantined-conflict');

  assert.equal(await store.addSegment('f', 2, 0, v2), 'published');
  assert.deepEqual(await fs.readFile(store.publishedPath('f')), v2);
});

test('recovery after flush preserves progress and fills remaining gaps', async () => {
  const dir = await tempDir();
  const content = randomBytes(1024);

  const first = await ReassemblyStore.open(dir);
  await first.declareFile('f', 1, content.length, sha256(content));
  await first.addSegment('f', 1, 0, content.subarray(0, 400));
  await first.addSegment('f', 1, 700, content.subarray(700, 1024));
  await first.flush(); // clean shutdown

  // Simulate restart: a brand-new store over the same directory.
  const second = await ReassemblyStore.open(dir);
  const info = second.getVersionInfo('f', 1);
  assert.equal(info?.state, 'receiving');
  assert.equal(info?.receivedBytes, 400 + (1024 - 700));
  assert.deepEqual(info?.gaps, [[400, 700]]);

  assert.equal(await second.addSegment('f', 1, 400, content.subarray(400, 700)), 'published');
  assert.deepEqual(await fs.readFile(second.publishedPath('f')), content);
});

test('checkpoint does not trust unsynced (unflushed) data', async () => {
  const dir = await tempDir();
  const content = randomBytes(768);

  const first = await ReassemblyStore.open(dir);
  await first.declareFile('f', 1, content.length, sha256(content));
  await first.addSegment('f', 1, 0, content.subarray(0, 256));
  await first.flush(); // first half is durable
  await first.addSegment('f', 1, 256, content.subarray(256, 512));
  // "Crash": no flush() — the second half only exists in memory.

  const second = await ReassemblyStore.open(dir);
  const info = second.getVersionInfo('f', 1);
  assert.equal(info?.state, 'receiving');
  assert.equal(info?.receivedBytes, 256, 'unflushed bytes must not appear in the checkpoint');
  assert.deepEqual(info?.gaps, [[256, 768]]);

  // The gap can simply be re-delivered after recovery.
  assert.equal(await second.addSegment('f', 1, 256, content.subarray(256, 512)), 'stored');
  assert.equal(await second.addSegment('f', 1, 512, content.subarray(512, 768)), 'published');
  assert.deepEqual(await fs.readFile(second.publishedPath('f')), content);
});

test('published state survives recovery', async () => {
  const dir = await tempDir();
  const content = randomBytes(128);
  const first = await ReassemblyStore.open(dir);
  await first.declareFile('f', 1, content.length, sha256(content));
  assert.equal(await first.addSegment('f', 1, 0, content), 'published');

  const second = await ReassemblyStore.open(dir);
  assert.equal(second.getVersionInfo('f', 1)?.state, 'published');
  assert.deepEqual(await fs.readFile(second.publishedPath('f')), content);
});

test('memory budget forces spill to disk without losing data', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir, { maxBufferedBytes: 1024 });
  const content = randomBytes(8192);
  await store.declareFile('f', 1, content.length, sha256(content));

  for (let offset = 0; offset < content.length; offset += 512) {
    const result = await store.addSegment('f', 1, offset, content.subarray(offset, offset + 512));
    assert.ok(store.stats().bufferedBytes <= 1024, `buffered ${store.stats().bufferedBytes} exceeds budget`);
    if (offset + 512 < content.length) assert.equal(result, 'stored');
    else assert.equal(result, 'published');
  }
  assert.deepEqual(await fs.readFile(store.publishedPath('f')), content);
});

test('a single segment larger than the memory budget is written through', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir, { maxBufferedBytes: 64 });
  const content = randomBytes(5000);
  await store.declareFile('f', 1, content.length, sha256(content));
  assert.equal(await store.addSegment('f', 1, 0, content), 'published');
  assert.deepEqual(await fs.readFile(store.publishedPath('f')), content);
});

test('conflict detection reads through spilled (on-disk) data, not just memory', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir, { maxBufferedBytes: 100 });
  const content = makeContent(300);
  await store.declareFile('f', 1, content.length, sha256(content));
  await store.addSegment('f', 1, 0, content.subarray(0, 150)); // forces spill (>100 bytes buffered)
  await store.flush();

  const tampered = Buffer.from(content.subarray(100, 300));
  tampered[10] = tampered[10]! ^ 0xff; // inside the on-disk [0,150) range
  assert.equal(await store.addSegment('f', 1, 100, tampered), 'quarantined-conflict');
});

test('declaring the same version twice with identical parameters is idempotent', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  const content = makeContent(10);
  await store.declareFile('f', 1, content.length, sha256(content));
  await store.declareFile('f', 1, content.length, sha256(content));
  await assert.rejects(store.declareFile('f', 1, 999, sha256(content)), /conflicting declaration/);
});

test('segments for undeclared versions are rejected', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open(dir);
  assert.equal(await store.addSegment('nope', 1, 0, Buffer.from('x')), 'rejected-unknown-version');
});
