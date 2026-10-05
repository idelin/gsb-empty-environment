import { BlockCache } from '../src/cache';
import { QueryEngine } from '../src/engine';
import { SetStore } from '../src/store';
import { and, or, setRef } from '../src/expr';
import { loadSetIds, referenceEval, validatePages } from '../src/validate';
import { assert, run, test } from './harness';
import { randomUniqueIds, range, tmpDir, writeSet, writeSetWithShards } from './helpers';

const QUOTA = 1 << 20;

function makeEngine(cacheBytes = QUOTA): { engine: QueryEngine; cache: BlockCache } {
  const cache = new BlockCache(cacheBytes);
  return { engine: new QueryEngine(new SetStore(cache)), cache };
}

function collectAllPages(
  engine: QueryEngine,
  expr: ReturnType<typeof or>,
  inputs: Record<string, string>,
  k: number,
  quota = QUOTA,
): { pages: number[][]; peaks: number[] } {
  const pages: number[][] = [];
  const peaks: number[] = [];
  let cursor: string | undefined;
  for (;;) {
    const page = engine.queryPage({ expr, inputs, k, cursor, quotaBytes: quota });
    pages.push(page.ids);
    peaks.push(page.stats.peakDecodedBytes);
    if (page.nextCursor === null) break;
    cursor = page.nextCursor;
  }
  return { pages, peaks };
}

test('OR of two sets pages in ascending order and matches reference', () => {
  const dir = tmpDir();
  const idsA = randomUniqueIds(1000, 5000, 1);
  const idsB = randomUniqueIds(1000, 5000, 2);
  const fileA = writeSet(dir, 'A', 'v1', idsA, 100);
  const fileB = writeSet(dir, 'B', 'v1', idsB, 128);
  const { engine } = makeEngine();
  const expr = or(setRef('A'), setRef('B'));
  const { pages, peaks } = collectAllPages(engine, expr, { A: fileA, B: fileB }, 97);
  const reference = referenceEval(expr, new Map([['A', idsA], ['B', idsB]]));
  assert.deepEqual(validatePages(pages, reference), []);
  // streaming bound: an OR of two sets never pins more than one block per leaf
  for (const p of peaks) assert.ok(p <= 2 * 512, 'peak ' + p + ' exceeds two decoded blocks');
  assert.ok(pages.length > 3, 'expected several pages');
});

test('AND across shards with empty shards matches reference', () => {
  const dir = tmpDir();
  const fileA = writeSetWithShards(dir, 'A', 'v1', [
    { rangeStart: 0, rangeEnd: 100, ids: range(0, 100, 2) },
    { rangeStart: 100, rangeEnd: 200, ids: [] },
    { rangeStart: 200, rangeEnd: 300, ids: range(200, 300, 2) },
  ]);
  const fileB = writeSet(dir, 'B', 'v1', range(40, 260, 3), 64);
  const { engine } = makeEngine();
  const expr = and(setRef('A'), setRef('B'));
  const { pages } = collectAllPages(engine, expr, { A: fileA, B: fileB }, 7);
  const reference = referenceEval(expr, new Map([
    ['A', loadSetIds(fileA)],
    ['B', loadSetIds(fileB)],
  ]));
  assert.deepEqual(validatePages(pages, reference), []);
});

test('nested and/or expression matches reference', () => {
  const dir = tmpDir();
  const idsA = randomUniqueIds(800, 10000, 11);
  const idsB = randomUniqueIds(800, 10000, 22);
  const idsC = randomUniqueIds(800, 10000, 33);
  const fileA = writeSet(dir, 'A', 'v1', idsA, 128);
  const fileB = writeSet(dir, 'B', 'v1', idsB, 128);
  const fileC = writeSet(dir, 'C', 'v1', idsC, 128);
  const { engine } = makeEngine();
  const expr = or(and(setRef('A'), setRef('B')), setRef('C'));
  const { pages } = collectAllPages(engine, expr, { A: fileA, B: fileB, C: fileC }, 50);
  const reference = referenceEval(expr, new Map([['A', idsA], ['B', idsB], ['C', idsC]]));
  assert.deepEqual(validatePages(pages, reference), []);
});

test('duplicate page requests with the same cursor return identical ids', () => {
  const dir = tmpDir();
  const fileA = writeSet(dir, 'A', 'v1', range(0, 500), 100);
  const { engine } = makeEngine();
  const expr = setRef('A');
  const page1 = engine.queryPage({ expr, inputs: { A: fileA }, k: 100, quotaBytes: QUOTA });
  const page2a = engine.queryPage({ expr, inputs: { A: fileA }, cursor: page1.nextCursor as string, quotaBytes: QUOTA });
  const page2b = engine.queryPage({ expr, inputs: { A: fileA }, cursor: page1.nextCursor as string, quotaBytes: QUOTA });
  assert.deepEqual(page2a.ids, page2b.ids);
  assert.deepEqual(page2a.ids, range(100, 200));
  assert.equal(page2a.nextCursor, page2b.nextCursor);
});

test('cancel mid-stream releases memory and re-read neither loses nor duplicates', () => {
  const dir = tmpDir();
  const fileA = writeSet(dir, 'A', 'v1', range(0, 1000), 100);
  const { engine } = makeEngine();
  const expr = setRef('A');

  const page1 = engine.queryPage({ expr, inputs: { A: fileA }, k: 250, quotaBytes: QUOTA });
  // start page 2, pull a few ids, then cancel
  const partial = engine.openStream({ expr, inputs: { A: fileA }, cursor: page1.nextCursor as string, quotaBytes: QUOTA });
  const pulled = [partial.next(), partial.next(), partial.next()];
  assert.deepEqual(pulled, [250, 251, 252]);
  partial.close();
  assert.equal(partial.session.usedBytes, 0, 'cancellation must release pinned decoded bytes');

  // re-read page 2 from the same cursor: identical to an uninterrupted read
  const { engine: fresh } = makeEngine();
  const reread = fresh.queryPage({ expr, inputs: { A: fileA }, cursor: page1.nextCursor as string, quotaBytes: QUOTA });
  assert.deepEqual(reread.ids, range(250, 500));

  // full pagination after the cancel is still complete and duplicate-free
  const { pages } = collectAllPages(fresh, expr, { A: fileA }, 250);
  assert.deepEqual(validatePages(pages, range(0, 1000)), []);
});

test('empty result yields one empty page and null cursor', () => {
  const dir = tmpDir();
  const fileA = writeSet(dir, 'A', 'v1', range(0, 100), 50);
  const fileB = writeSet(dir, 'B', 'v1', range(1000, 1100), 50);
  const { engine } = makeEngine();
  const expr = and(setRef('A'), setRef('B'));
  const page = engine.queryPage({ expr, inputs: { A: fileA, B: fileB }, k: 10, quotaBytes: QUOTA });
  assert.deepEqual(page.ids, []);
  assert.equal(page.nextCursor, null);
  assert.deepEqual(validatePages([page.ids], []), []);
});

test('huge sparse id gaps page correctly', () => {
  const dir = tmpDir();
  const ids = [0, 1, 2147483648, 4294967294, 4294967295];
  const fileA = writeSet(dir, 'S', 'v1', ids, 2);
  const { engine } = makeEngine();
  const { pages } = collectAllPages(engine, setRef('S'), { S: fileA }, 2);
  assert.deepEqual(pages, [[0, 1], [2147483648, 4294967294], [4294967295]]);
  assert.deepEqual(validatePages(pages, ids), []);
});

test('result larger than the decoded-bytes quota still streams correctly', () => {
  const dir = tmpDir();
  const idsA = range(0, 2000);
  const fileA = writeSet(dir, 'A', 'v1', idsA, 256);
  const { engine } = makeEngine();
  // quota fits only a single 512-byte block, result is 2000 ids
  const { pages, peaks } = collectAllPages(engine, setRef('A'), { A: fileA }, 128, 512);
  assert.deepEqual(validatePages(pages, idsA), []);
  for (const p of peaks) assert.ok(p <= 512, 'peak ' + p + ' exceeds quota');
});

run();
