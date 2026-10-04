import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import { join } from 'node:path';
import {
  ReassemblyStore,
  SegmentConflictError,
  SegmentOutOfBoundsError,
  VersionNotWritableError,
} from '../src/index.ts';
import type { VersionEvent } from '../src/index.ts';
import { content, seg, sha256, specFor, tempDir } from './helpers.ts';

test('empty file publishes immediately on registration', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open({ dir });

  const info = await store.registerFile(specFor('empty.bin', 'v1', Buffer.alloc(0)));
  assert.equal(info.state, 'published');
  assert.equal(info.coveredBytes, 0);

  const published = await fs.readFile(join(dir, 'published', 'empty.bin'));
  assert.equal(published.length, 0);

  await assert.rejects(
    store.addSegment({ fileId: 'empty.bin', version: 'v1', offset: 0, data: Buffer.alloc(0) }),
    VersionNotWritableError,
  );
  await store.close();
});

test('out-of-bounds segments are rejected and the version stays usable', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open({ dir });
  const data = content(10, 1);
  await store.registerFile(specFor('f.bin', 'v1', data));

  await assert.rejects(
    store.addSegment({ fileId: 'f.bin', version: 'v1', offset: 8, data: content(4, 2) }),
    SegmentOutOfBoundsError,
  );
  await assert.rejects(
    store.addSegment({ fileId: 'f.bin', version: 'v1', offset: -1, data: content(2, 3) }),
    SegmentOutOfBoundsError,
  );
  await assert.rejects(
    store.addSegment({ fileId: 'f.bin', version: 'v1', offset: 11, data: Buffer.alloc(0) }),
    SegmentOutOfBoundsError,
  );

  // Zero-length segment exactly at end of file is a harmless no-op.
  assert.equal(
    await store.addSegment({ fileId: 'f.bin', version: 'v1', offset: 10, data: Buffer.alloc(0) }),
    'noop',
  );

  assert.equal(store.getVersion('f.bin', 'v1')?.state, 'active');
  assert.equal(await store.addSegment(seg('f.bin', 'v1', data, 0, 5)), 'stored');
  assert.equal(await store.addSegment(seg('f.bin', 'v1', data, 5, 10)), 'published');
  assert.deepEqual(await fs.readFile(join(dir, 'published', 'f.bin')), data);
  await store.close();
});

test('out-of-order, overlapping and duplicate segments reassemble correctly', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open({ dir });
  const data = content(1024, 7);
  await store.registerFile(specFor('big.bin', 'v1', data));

  assert.equal(await store.addSegment(seg('big.bin', 'v1', data, 512, 768)), 'stored');
  assert.equal(await store.addSegment(seg('big.bin', 'v1', data, 0, 256)), 'stored');
  // Consistent overlap with both neighbors.
  assert.equal(await store.addSegment(seg('big.bin', 'v1', data, 100, 300)), 'stored');
  // Exact duplicate of an already-covered range.
  assert.equal(await store.addSegment(seg('big.bin', 'v1', data, 0, 256)), 'duplicate');
  assert.equal(await store.addSegment(seg('big.bin', 'v1', data, 256, 512)), 'stored');
  assert.equal(await store.addSegment(seg('big.bin', 'v1', data, 900, 1024)), 'stored');
  // Partially overlapping tail segment completes coverage.
  assert.equal(await store.addSegment(seg('big.bin', 'v1', data, 768, 1024)), 'published');

  assert.deepEqual(await fs.readFile(join(dir, 'published', 'big.bin')), data);
  await store.close();
});

test('conflicting overlap quarantines only that version, durably', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open({ dir });
  const good = content(100, 11);
  const evil = content(100, 12);
  await store.registerFile(specFor('f.bin', 'v1', good));
  await store.registerFile(specFor('other.bin', 'v1', good));

  assert.equal(await store.addSegment(seg('f.bin', 'v1', good, 0, 50)), 'stored');
  // Overlaps [25, 50) with different bytes.
  await assert.rejects(
    store.addSegment(seg('f.bin', 'v1', evil, 25, 75)),
    (err: unknown) => {
      assert.ok(err instanceof SegmentConflictError);
      assert.equal(err.conflictOffset, 25);
      return true;
    },
  );

  const info = store.getVersion('f.bin', 'v1');
  assert.equal(info?.state, 'quarantined');
  assert.match(info?.reason ?? '', /conflict/);
  await assert.rejects(
    store.addSegment(seg('f.bin', 'v1', good, 50, 100)),
    VersionNotWritableError,
  );

  // Quarantine survives a restart; the sibling file is unaffected.
  await store.close();
  const reopened = await ReassemblyStore.open({ dir });
  assert.equal(reopened.getVersion('f.bin', 'v1')?.state, 'quarantined');
  await assert.rejects(
    reopened.addSegment(seg('f.bin', 'v1', good, 50, 100)),
    VersionNotWritableError,
  );
  assert.equal(await reopened.addSegment(seg('other.bin', 'v1', good, 0, 100)), 'published');
  await reopened.close();
});

test('interleaved versions reassemble independently', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open({ dir });
  const a = content(300, 21);
  const b = content(500, 22);
  const c = content(200, 23);
  const cBad = content(200, 24);
  await store.registerFile(specFor('file.bin', 'v1', a));
  await store.registerFile(specFor('file.bin', 'v2', b));
  await store.registerFile(specFor('file.bin', 'v3', c));

  const published: VersionEvent[] = [];
  store.on('published', (event: VersionEvent) => published.push(event));

  // Interleave delivery across all three versions.
  await store.addSegment(seg('file.bin', 'v1', a, 0, 150));
  await store.addSegment(seg('file.bin', 'v2', b, 250, 500));
  await store.addSegment(seg('file.bin', 'v3', c, 0, 100));
  await store.addSegment(seg('file.bin', 'v2', b, 0, 250));
  await store.addSegment(seg('file.bin', 'v1', a, 150, 300));

  // v3 poisons itself with a conflicting duplicate; only v3 is quarantined.
  await assert.rejects(
    store.addSegment(seg('file.bin', 'v3', cBad, 50, 150)),
    SegmentConflictError,
  );
  assert.equal(store.getVersion('file.bin', 'v3')?.state, 'quarantined');
  assert.equal(store.getVersion('file.bin', 'v1')?.state, 'published');
  assert.equal(store.getVersion('file.bin', 'v2')?.state, 'published');

  assert.deepEqual(published.map((e) => (e.type === 'published' ? e.info.version : '')), ['v2', 'v1']);
  // v1 published last, so it owns the published path.
  assert.deepEqual(await fs.readFile(join(dir, 'published', 'file.bin')), a);
  await store.close();
});

test('restart recovers checkpointed coverage and fills the gaps', async () => {
  const dir = await tempDir();
  const data = content(2000, 31);
  const chunk = 200;

  const first = await ReassemblyStore.open({ dir });
  await first.registerFile(specFor('f.bin', 'v1', data));
  for (let start = 0; start < 1000; start += chunk) {
    assert.equal(await first.addSegment(seg('f.bin', 'v1', data, start, start + chunk)), 'stored');
  }
  await first.close();

  // A fresh store on the same directory resumes from the checkpoint.
  const second = await ReassemblyStore.open({ dir });
  const resumed = second.getVersion('f.bin', 'v1');
  assert.equal(resumed?.state, 'active');
  assert.equal(resumed?.coveredBytes, 1000);

  let outcome = '';
  for (let start = 1000; start < 2000; start += chunk) {
    outcome = await second.addSegment(seg('f.bin', 'v1', data, start, start + chunk));
  }
  assert.equal(outcome, 'published');
  assert.deepEqual(await fs.readFile(join(dir, 'published', 'f.bin')), data);
  await second.close();
});

test('checkpoint never trusts data that was not synced and recorded', async () => {
  const dir = await tempDir();
  const data = content(1000, 41);
  const chunk = 200;
  let crashed = false;

  const first = await ReassemblyStore.open({
    dir,
    hooks: {
      beforeCheckpointWrite: () => {
        if (crashed) throw new Error('simulated crash');
      },
    },
  });
  await first.registerFile(specFor('f.bin', 'v1', data));
  await first.addSegment(seg('f.bin', 'v1', data, 0, chunk));

  // Crash after the segment bytes hit disk but before the checkpoint records them.
  crashed = true;
  await assert.rejects(first.addSegment(seg('f.bin', 'v1', data, chunk, 2 * chunk)), /simulated crash/);

  // Simulate a torn trailing write: garbage bytes beyond the recorded coverage.
  const dataFiles = await fs.readdir(join(dir, 'data'));
  assert.equal(dataFiles.length, 1);
  const handle = await fs.open(join(dir, 'data', dataFiles[0]), 'r+');
  await handle.write(content(200, 99), 0, 200, 2 * chunk);
  await handle.close();

  // Recovery trusts only the checkpointed [0, 200): the other 400 bytes on disk are ignored.
  const second = await ReassemblyStore.open({ dir });
  const resumed = second.getVersion('f.bin', 'v1');
  assert.equal(resumed?.state, 'active');
  assert.equal(resumed?.coveredBytes, chunk);

  // Re-deliver everything past the checkpoint; the garbage gets overwritten and
  // the final digest still verifies.
  let outcome = '';
  for (let start = chunk; start < 1000; start += chunk) {
    outcome = await second.addSegment(seg('f.bin', 'v1', data, start, start + chunk));
  }
  assert.equal(outcome, 'published');
  assert.deepEqual(await fs.readFile(join(dir, 'published', 'f.bin')), data);
  await second.close();
});

test('digest mismatch after full coverage fails instead of publishing', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open({ dir });
  const data = content(500, 51);
  await store.registerFile({
    fileId: 'f.bin',
    version: 'v1',
    size: data.length,
    digest: { algorithm: 'sha256', hex: sha256('something else') },
  });

  assert.equal(await store.addSegment(seg('f.bin', 'v1', data, 0, 500)), 'failed');
  const info = store.getVersion('f.bin', 'v1');
  assert.equal(info?.state, 'failed');
  assert.match(info?.reason ?? '', /digest mismatch/);
  assert.deepEqual(await fs.readdir(join(dir, 'published')), []);
  await assert.rejects(
    store.addSegment(seg('f.bin', 'v1', data, 0, 100)),
    VersionNotWritableError,
  );
  await store.close();
});

test('memory budget is a hard cap; oversized segments spill to disk', async () => {
  const dir = await tempDir();
  const store = await ReassemblyStore.open({ dir, maxMemoryBytes: 2048, chunkBytes: 256 });
  const data = content(8192, 61);
  await store.registerFile(specFor('f.bin', 'v1', data));

  // One segment larger than the whole budget: must take the spill path.
  const big = store.addSegment(seg('f.bin', 'v1', data, 0, 4096));
  // Many concurrent in-memory segments: the budget forces queueing.
  const rest: Promise<unknown>[] = [];
  for (let start = 4096; start < 8192; start += 512) {
    rest.push(store.addSegment(seg('f.bin', 'v1', data, start, start + 512)));
  }
  const outcomes = await Promise.all([big, ...rest]);
  assert.ok(outcomes.includes('published'));

  assert.ok(store.memoryHighWaterBytes > 0);
  assert.ok(
    store.memoryHighWaterBytes <= 2048,
    `high water ${store.memoryHighWaterBytes} exceeds budget`,
  );
  assert.deepEqual(await fs.readFile(join(dir, 'published', 'f.bin')), data);
  // Spill directory is cleaned up after use.
  assert.deepEqual(await fs.readdir(join(dir, 'spill')), []);
  await store.close();
});
