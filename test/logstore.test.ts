import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Buffer } from 'node:buffer';
import { LogStore, fdIsOpen } from '../src/logstore.ts';
import { scanFrames } from '../src/codec.ts';
import { readFileSync } from 'node:fs';

function tmp(): string {
  return mkdtempSync(join(tmpdir(), 'logstore-'));
}

test('append and zero-copy readback', async () => {
  const store = LogStore.open(join(tmp(), 'a.log'));
  const v1 = await store.append({ term: 1n, index: 1n, payload: Buffer.from('first') });
  const v2 = await store.append({ term: 1n, index: 2n, payload: Buffer.from('second') });
  assert.equal(store.lastIndex, 2);
  assert.equal(store.lastTerm, 1n);
  assert.deepEqual(store.at(1)?.payload, Buffer.from('first'));
  assert.deepEqual(v2.payload, Buffer.from('second'));
  // Zero-copy: two reads of the same slot share the same underlying memory.
  const r1 = store.at(1);
  const r2 = store.at(1);
  assert.ok(r1 && r2);
  assert.strictEqual(r1.payload.buffer, r2.payload.buffer);
  assert.strictEqual(v1.payload.buffer, r1.payload.buffer);
  await store.close();
  assert.ok(!fdIsOpen(store));
});

test('persistence: reopen recovers every entry byte-for-byte', async () => {
  const path = join(tmp(), 'persist.log');
  const store = LogStore.open(path);
  for (let i = 1; i <= 50; i++) {
    await store.append({ term: BigInt(i), index: BigInt(i), payload: Buffer.from(`value-${i}`) });
  }
  await store.close();

  const reopened = LogStore.open(path);
  assert.equal(reopened.lastIndex, 50);
  for (let i = 1; i <= 50; i++) {
    const v = reopened.at(i);
    assert.ok(v);
    assert.equal(v.term, BigInt(i));
    assert.deepEqual(v.payload, Buffer.from(`value-${i}`));
  }
  await reopened.close();
});

test('truncateSuffix drops slots and shrinks the file', async () => {
  const path = join(tmp(), 'trunc.log');
  const store = LogStore.open(path);
  for (let i = 1; i <= 5; i++) {
    await store.append({ term: 1n, index: BigInt(i), payload: Buffer.from(`v${i}`) });
  }
  const sizeBefore = statSync(path).size;
  await store.truncateSuffix(4);
  assert.equal(store.lastIndex, 3);
  assert.equal(store.at(4), undefined);
  assert.ok(statSync(path).size < sizeBefore);
  // File on disk parses cleanly up to exactly 3 frames.
  const views = [...scanFrames(readFileSync(path))];
  assert.equal(views.length, 3);
  // Appends after truncation land at the cut offset.
  await store.append({ term: 2n, index: 4n, payload: Buffer.from('rewritten') });
  assert.deepEqual(store.at(4)?.payload, Buffer.from('rewritten'));
  await store.close();
  const reopened = LogStore.open(path);
  assert.equal(reopened.lastIndex, 4);
  assert.deepEqual(reopened.at(4)?.payload, Buffer.from('rewritten'));
  await reopened.close();
});

test('concurrent appends are serialized into a strict total order', async () => {
  const path = join(tmp(), 'conc.log');
  const store = LogStore.open(path);
  const N = 300;
  await Promise.all(
    Array.from({ length: N }, (_, i) =>
      store.append({ term: 1n, index: BigInt(i + 1), payload: Buffer.from(`c${i + 1}`) }),
    ),
  );
  assert.equal(store.lastIndex, N);
  for (let i = 1; i <= N; i++) {
    assert.deepEqual(store.at(i)?.payload, Buffer.from(`c${i}`));
  }
  await store.close();
  // On-disk image is intact and contiguous.
  const views = [...scanFrames(readFileSync(path))];
  assert.equal(views.length, N);
  assert.deepEqual(views.map((v) => Number(v.index)), Array.from({ length: N }, (_, i) => i + 1));
});

test('concurrent append + truncateSuffix never corrupts the segment', async () => {
  const path = join(tmp(), 'mix.log');
  const store = LogStore.open(path);
  const ops: Promise<unknown>[] = [];
  for (let i = 1; i <= 100; i++) {
    ops.push(store.append({ term: 1n, index: BigInt(i), payload: Buffer.from(`m${i}`) }));
  }
  ops.push(store.truncateSuffix(51));
  await Promise.all(ops);
  await store.close();
  // Whatever the interleaving, the file must parse and hold a prefix of slots.
  const views = [...scanFrames(readFileSync(path))];
  const indices = views.map((v) => Number(v.index));
  const sorted = [...indices].sort((a, b) => a - b);
  assert.deepEqual(indices, sorted);
  assert.ok(indices.length >= 50 && indices.length <= 100);
  assert.deepEqual(indices.slice(0, 50), Array.from({ length: 50 }, (_, i) => i + 1));
});

test('operations after close are rejected (no fd leak, no use-after-close)', async () => {
  const store = LogStore.open(join(tmp(), 'closed.log'));
  await store.append({ term: 1n, index: 1n, payload: Buffer.from('x') });
  await store.close();
  assert.ok(store.isClosed);
  assert.ok(!fdIsOpen(store));
  await assert.rejects(() => store.append({ term: 1n, index: 2n }), /closed/);
  await assert.rejects(() => store.truncateSuffix(1), /closed/);
  // Double close is a no-op.
  await store.close();
});
