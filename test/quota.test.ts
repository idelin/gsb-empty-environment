import { BlockCache } from '../src/cache';
import { QueryEngine, QuotaExceededError } from '../src/engine';
import { SetStore } from '../src/store';
import { and, setRef } from '../src/expr';
import { assert, run, test } from './harness';
import { range, tmpDir, writeSet } from './helpers';

const BLOCK_BYTES = 128 * 4; // one full decoded block = 512 bytes

function makeShared(): { engine: QueryEngine; store: SetStore; cache: BlockCache } {
  const cache = new BlockCache(1 << 20);
  const store = new SetStore(cache);
  return { engine: new QueryEngine(store), store, cache };
}

test('quota smaller than a single minimal block is explicitly rejected', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'A', 'v1', range(0, 500), 200);
  const { engine } = makeShared();
  assert.throws(
    () => engine.queryPage({ expr: setRef('A'), inputs: { A: file }, k: 10, quotaBytes: 100 }),
    (e: Error) =>
      e instanceof QuotaExceededError &&
      e.message.includes('single minimal block') &&
      e.message.includes('100'),
  );
});

test('quota of exactly one block pages a whole set, peak equals one block', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'A', 'v1', range(0, 1000), 128);
  const { engine } = makeShared();
  let cursor: string | undefined;
  const all: number[] = [];
  let peak = 0;
  for (;;) {
    const page = engine.queryPage({ expr: setRef('A'), inputs: { A: file }, k: 100, cursor, quotaBytes: BLOCK_BYTES });
    all.push(...page.ids);
    peak = Math.max(peak, page.stats.peakDecodedBytes);
    if (page.nextCursor === null) break;
    cursor = page.nextCursor;
  }
  assert.deepEqual(all, range(0, 1000));
  assert.equal(peak, BLOCK_BYTES);
});

test('AND of three sets needs three concurrent blocks; quota enforced accordingly', () => {
  const dir = tmpDir();
  const fileA = writeSet(dir, 'A', 'v1', range(0, 500, 2), 250);
  const fileB = writeSet(dir, 'B', 'v1', range(0, 500, 3), 250);
  const fileC = writeSet(dir, 'C', 'v1', range(0, 1000, 5), 250);
  const { engine } = makeShared();
  const expr = and(setRef('A'), setRef('B'), setRef('C'));
  const inputs = { A: fileA, B: fileB, C: fileC };

  assert.throws(
    () => engine.queryPage({ expr, inputs, k: 10, quotaBytes: BLOCK_BYTES }),
    QuotaExceededError,
  );
  const page = engine.queryPage({ expr, inputs, k: 10, quotaBytes: 3 * BLOCK_BYTES });
  assert.equal(page.stats.peakDecodedBytes, 3 * BLOCK_BYTES);
  const expected = range(0, 500, 30).slice(0, 10);
  assert.deepEqual(page.ids, expected);
});

test('multi-tenant: shared cache, independent quotas', () => {
  const dir = tmpDir();
  const fileA = writeSet(dir, 'A', 'v1', range(0, 2000), 256);
  const { engine, cache } = makeShared();
  const expr = setRef('A');
  const inputs = { A: fileA };

  // tenant "rich" reads first: decodes blocks from disk into the shared cache
  const first = engine.queryPage({ expr, inputs, k: 128, quotaBytes: BLOCK_BYTES, tenant: 'rich' });
  assert.deepEqual(first.ids, range(0, 128));
  assert.ok(first.stats.blocksDecoded > 0);
  const hitsAfterFirst = cache.hits;

  // tenant "poor" cannot fit even one decoded block, before and after rich's reads:
  // quotas are accounted per tenant session, independently of the shared cache
  assert.throws(
    () => engine.queryPage({ expr, inputs, k: 10, quotaBytes: 64, tenant: 'poor' }),
    QuotaExceededError,
  );
  assert.throws(
    () => engine.queryPage({ expr, inputs, k: 10, quotaBytes: 64, tenant: 'poor' }),
    QuotaExceededError,
  );

  // second read by another tenant hits the shared cache and decodes nothing
  const second = engine.queryPage({ expr, inputs, k: 128, quotaBytes: BLOCK_BYTES, tenant: 'rich2' });
  assert.deepEqual(second.ids, range(0, 128));
  assert.equal(second.stats.blocksDecoded, 0);
  assert.ok(cache.hits > hitsAfterFirst, 'expected shared-cache hits');

  // per-tenant stats are reported independently
  assert.equal(first.stats.tenant, 'rich');
  assert.equal(second.stats.tenant, 'rich2');
  assert.equal(first.stats.peakDecodedBytes, BLOCK_BYTES);
  assert.equal(second.stats.peakDecodedBytes, BLOCK_BYTES);
});

test('quota failure mid-page leaves no state; retry with same cursor succeeds', () => {
  const dir = tmpDir();
  const fileA = writeSet(dir, 'A', 'v1', range(0, 1000), 128);
  const fileB = writeSet(dir, 'B', 'v1', range(0, 1000), 128);
  const { engine } = makeShared();
  const expr = and(setRef('A'), setRef('B'));
  const inputs = { A: fileA, B: fileB };

  const page1 = engine.queryPage({ expr, inputs, k: 100, quotaBytes: 2 * BLOCK_BYTES });
  assert.deepEqual(page1.ids, range(0, 100));

  // second page attempted with a quota that cannot hold two blocks: rejected
  assert.throws(
    () => engine.queryPage({ expr, inputs, cursor: page1.nextCursor as string, quotaBytes: BLOCK_BYTES }),
    QuotaExceededError,
  );

  // retry with adequate quota and the same cursor: exact continuation, no loss/dup
  const page2 = engine.queryPage({ expr, inputs, cursor: page1.nextCursor as string, quotaBytes: 2 * BLOCK_BYTES });
  assert.deepEqual(page2.ids, range(100, 200));
});

run();
