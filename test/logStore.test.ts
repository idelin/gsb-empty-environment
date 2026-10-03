import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { ENTRY_VALUE, makeEntry } from '../src/codec/logEntry.ts';
import { LogStore } from '../src/storage/logStore.ts';
import { resources } from '../src/storage/resources.ts';

async function withStore<T>(fn: (dir: string) => Promise<T>): Promise<T> {
  const dir = await mkdtemp(join(tmpdir(), 'store-'));
  try {
    return await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
    assert.equal(resources.outstanding('store'), 0, 'store leak');
    assert.equal(resources.outstanding('segment'), 0, 'segment leak');
  }
}

test('store persists entries and promised ballot across reopen', () =>
  withStore(async (dir) => {
    const s1 = await LogStore.open(dir);
    await s1.setPromised({ term: 4, nodeId: 2 });
    await s1.accept(makeEntry(1, 4, 2, ENTRY_VALUE, Buffer.from('alpha')));
    await s1.accept(makeEntry(2, 4, 2, ENTRY_VALUE, Buffer.from('beta')));
    await s1.setCommitIndex(1);
    await s1.close();

    const s2 = await LogStore.open(dir);
    assert.deepEqual(s2.promised, { term: 4, nodeId: 2 });
    assert.equal(s2.commitIndex, 1);
    assert.equal(s2.maxIndex, 2);
    assert.equal(s2.get(1)!.value.toString(), 'alpha');
    assert.equal(s2.get(2)!.value.toString(), 'beta');
    assert.equal(s2.contiguousIndex, 2);
    await s2.close();
  }));

test('concurrent accepts are serialized and all durable', () =>
  withStore(async (dir) => {
    const s = await LogStore.open(dir);
    const N = 200;
    await Promise.all(
      Array.from({ length: N }, (_, i) => s.accept(makeEntry(i + 1, 1, 1, ENTRY_VALUE, Buffer.from(`v${i}`)))),
    );
    assert.equal(s.pendingWrites, 0);
    assert.equal(s.maxIndex, N);
    await s.close();

    const s2 = await LogStore.open(dir);
    for (let i = 1; i <= N; i++) assert.equal(s2.get(i)!.value.toString(), `v${i - 1}`);
    await s2.close();
  }));

test('dirty slot overwrite keeps the last accepted value', () =>
  withStore(async (dir) => {
    const s = await LogStore.open(dir);
    await s.accept(makeEntry(3, 1, 1, ENTRY_VALUE, Buffer.from('dirty')));
    await s.accept(makeEntry(3, 2, 5, ENTRY_VALUE, Buffer.from('clean')));
    assert.equal(s.get(3)!.value.toString(), 'clean');
    assert.equal(s.get(3)!.term, 2);
    await s.close();

    const s2 = await LogStore.open(dir);
    assert.equal(s2.get(3)!.value.toString(), 'clean');
    await s2.close();
  }));

test('contiguousIndex stops at the first hole', () =>
  withStore(async (dir) => {
    const s = await LogStore.open(dir);
    await s.accept(makeEntry(1, 1, 1, ENTRY_VALUE, Buffer.from('a')));
    await s.accept(makeEntry(2, 1, 1, ENTRY_VALUE, Buffer.from('b')));
    await s.accept(makeEntry(4, 1, 1, ENTRY_VALUE, Buffer.from('d'))); // hole at 3
    assert.equal(s.contiguousIndex, 2);
    assert.equal(s.maxIndex, 4);
    assert.deepEqual(
      s.entriesFrom(2).map((e) => e.index),
      [2, 4],
    );
    await s.close();
  }));

test('store rejects writes after close and closes idempotently', () =>
  withStore(async (dir) => {
    const s = await LogStore.open(dir);
    await s.close();
    await s.close();
    await assert.rejects(() => s.accept(makeEntry(1, 1, 1, ENTRY_VALUE, Buffer.alloc(0))), /closed/);
  }));
