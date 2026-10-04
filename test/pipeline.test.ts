import {
  BatchReleasedError,
  BatchSource,
  BatchSpec,
  ColumnBatch,
  LoadCancelledError,
  Plan,
  PoolExhaustedError,
  QueryResult,
  StaleViewError,
  UnknownColumnError,
  Value,
  WideLoader,
  columnData,
  execute,
  filterBatch,
  runReference,
} from '../src/index';
import {
  assert,
  assertDeepEqual,
  assertEqual,
  assertRejects,
  assertThrows,
  test,
} from './harness';

function wideColumn(
  values: Value[],
  delayMs = 0,
  onLoad?: () => void,
): WideLoader {
  return ({ signal }) =>
    new Promise((resolve, reject) => {
      if (onLoad) onLoad();
      const timer = setTimeout(() => resolve(columnData(values)), delayMs);
      if (signal) {
        signal.addEventListener('abort', () => {
          clearTimeout(timer);
          reject(new LoadCancelledError('loader aborted by signal'));
        });
      }
    });
}

function assertSameResult(actual: QueryResult, expected: QueryResult): void {
  assertDeepEqual(actual.columns, expected.columns, 'columns');
  assertEqual(actual.rows.length, expected.rows.length, 'row count');
  for (let i = 0; i < actual.rows.length; i++) {
    assertDeepEqual(actual.rows[i], expected.rows[i], `row ${i}`);
  }
}

/**
 * Shared dataset: 3 batches, duplicate sort keys across batches, some
 * nulls in the narrow filter column, one projected wide column
 * (`payload`) and one wide column that is never projected (`blob`).
 */
function makeSpecs(loadCounter?: { payload: number; blob: number }): BatchSpec[] {
  const payload = (values: Value[], delayMs: number): WideLoader =>
    wideColumn(values, delayMs, loadCounter ? () => loadCounter.payload++ : undefined);
  const blob = (values: Value[]): WideLoader =>
    wideColumn(values, 0, loadCounter ? () => loadCounter.blob++ : undefined);
  return [
    {
      version: 1,
      rowIds: [1, 2, 3],
      rowVersions: [1, 1, 1],
      narrow: {
        k: { values: [2, 1, 2] },
        status: { values: [10, 20, null] },
      },
      wide: {
        payload: payload(['p1', 'p2', 'p3'], 30),
        blob: blob(['b1', 'b2', 'b3']),
      },
    },
    {
      version: 1,
      rowIds: [4, 5, 6],
      rowVersions: [1, 1, 2],
      narrow: {
        k: { values: [1, 2, 1] },
        status: { values: [10, null, 30] },
      },
      wide: {
        payload: payload(['p4', 'p5', 'p6'], 10),
        blob: blob(['b4', 'b5', 'b6']),
      },
    },
    {
      version: 2,
      rowIds: [7, 8],
      rowVersions: [3, 1],
      narrow: {
        k: { values: [2, 1] },
        status: { values: [10, 20] },
      },
      wide: {
        payload: payload(['p7', 'p8'], 20),
        blob: blob(['b7', 'b8']),
      },
    },
  ];
}

test('empty selection: filter matches nothing, no wide column is read', async () => {
  const counter = { payload: 0, blob: 0 };
  const source = new BatchSource(makeSpecs(counter), 3);
  const plan: Plan = {
    filter: { col: 'status', op: 'eq', value: 999 },
    project: ['status', 'payload'],
  };
  const result = await execute(source, plan);
  assertEqual(result.rows.length, 0, 'row count');
  assertEqual(counter.payload, 0, 'payload loads');
  assertEqual(counter.blob, 0, 'blob loads');
  assert(!source.stats.loads.has('payload'), 'stats must not record payload loads');
  assertSameResult(result, await runReference(makeSpecs(), plan));
});

test('all null: null filter column yields empty; null sort keys keep input order', async () => {
  const specs = (): BatchSpec[] => [
    {
      version: 1,
      rowIds: [1, 2],
      rowVersions: [1, 1],
      narrow: {
        status: { values: [null, null] },
        k: { values: [null, null] },
      },
      wide: { payload: wideColumn(['x', 'y']) },
    },
    {
      version: 1,
      rowIds: [3],
      rowVersions: [1],
      narrow: {
        status: { values: [null] },
        k: { values: [null] },
      },
      wide: { payload: wideColumn(['z']) },
    },
  ];

  const filtered: Plan = {
    filter: { col: 'status', op: 'eq', value: 1 },
    project: ['status', 'payload'],
  };
  const empty = await execute(new BatchSource(specs(), 2), filtered);
  assertEqual(empty.rows.length, 0, 'null = anything is unknown, row dropped');
  assertSameResult(empty, await runReference(specs(), filtered));

  const sorted: Plan = { sort: { key: 'k' }, project: ['k', 'payload'] };
  const ordered = await execute(new BatchSource(specs(), 2), sorted);
  assertDeepEqual(
    ordered.rows.map((r) => r.rowId),
    [1, 2, 3],
    'all-null keys tie and keep arrival order',
  );
  assertSameResult(ordered, await runReference(specs(), sorted));
});

test('cross-batch sort: globally ordered, ties keep input order despite delayed loads', async () => {
  const counter = { payload: 0, blob: 0 };
  const source = new BatchSource(makeSpecs(counter), 3);
  const plan: Plan = { sort: { key: 'k' }, project: ['k', 'payload'] };
  const result = await execute(source, plan);
  assertDeepEqual(
    result.rows.map((r) => r.rowId),
    [2, 4, 6, 8, 1, 3, 5, 7],
    'stable across batches: k=1 group then k=2 group, arrival order inside',
  );
  assertDeepEqual(
    result.rows.map((r) => r.values[1]),
    ['p2', 'p4', 'p6', 'p8', 'p1', 'p3', 'p5', 'p7'],
    'wide values stay attached to their rows',
  );
  assertEqual(counter.payload, 3, 'payload loaded once per non-empty batch');
  assertEqual(counter.blob, 0, 'unprojected wide column never read');
  assertSameResult(result, await runReference(makeSpecs(), plan));
});

test('duplicate rowID with different versions: rows stay distinct and correctly paired', async () => {
  const specs = (): BatchSpec[] => [
    {
      version: 7,
      rowIds: [7, 7, 7],
      rowVersions: [1, 2, 3],
      narrow: {
        k: { values: [5, 5, 5] },
        status: { values: [1, 1, 1] },
      },
      wide: { payload: wideColumn(['v1', 'v2', 'v3']) },
    },
  ];
  const plan: Plan = {
    filter: { col: 'status', op: 'eq', value: 1 },
    sort: { key: 'k' },
    project: ['payload'],
  };
  const result = await execute(new BatchSource(specs(), 1), plan);
  assertDeepEqual(
    result.rows.map((r) => [r.rowId, r.rowVersion, r.values[0]]),
    [
      [7, 1, 'v1'],
      [7, 2, 'v2'],
      [7, 3, 'v3'],
    ],
    'same rowID, different versions: all kept, stable order, values not crossed',
  );
  assertSameResult(result, await runReference(specs(), plan));
});

test('cancellation: aborting a wide load cancels the query and is counted', async () => {
  const counter = { payload: 0, blob: 0 };
  const source = new BatchSource(makeSpecs(counter), 3);
  const plan: Plan = { project: ['payload'] };
  const ac = new AbortController();
  const pending = execute(source, plan, { signal: ac.signal });
  setTimeout(() => ac.abort(), 5);
  await assertRejects(pending, LoadCancelledError, 'execute must reject');
  const st = source.stats.loads.get('payload');
  assert(st !== undefined, 'payload load must have been attempted');
  assertEqual(st!.started, 1, 'started');
  assertEqual(st!.cancelled, 1, 'cancelled');
  assertEqual(st!.completed, 0, 'completed');
  assertEqual(counter.blob, 0, 'blob never touched');
});

test('buffer reuse: recycled slot invalidates old views and selection vectors', async () => {
  const specs = makeSpecs();
  const source = new BatchSource(specs, 1);
  const it = source.batches();

  const b1 = (await it.next()).value as ColumnBatch;
  const view = b1.narrowView('status');
  const sel = filterBatch(b1, { col: 'status', op: 'gte', value: 0 });
  assertEqual(sel.count, 2, 'rows with non-null status in batch 1');
  assertEqual(view.valueAt(0), 10, 'view works while batch is alive');

  const b2 = (await it.next()).value as ColumnBatch;
  assert(b2.batchId !== b1.batchId, 'second emission is a new batch');
  assert(b1.released, 'old batch released by slot recycling');
  assertEqual(source.stats.batchesRecycled, 1, 'one recycling event');

  assertThrows(() => view.valueAt(0), BatchReleasedError, 'old view after release');
  assertThrows(() => sel.resolve(), BatchReleasedError, 'old selection after release');
  assertThrows(
    () => b1.narrowView('status'),
    BatchReleasedError,
    'old batch cannot serve new views',
  );
  assertEqual(b2.narrowView('status').valueAt(0), 10, 'new batch readable on reused slot');

  b2.release();
  assertThrows(() => b2.narrowView('k'), BatchReleasedError, 'explicit release');
});

test('pinned batch blocks slot recycling', async () => {
  const source = new BatchSource(makeSpecs(), 1);
  const it = source.batches();
  const b1 = (await it.next()).value as ColumnBatch;
  b1.pin();
  await assertRejects(it.next(), PoolExhaustedError, 'recycling a pinned slot');
  b1.unpin();
});

test('unprojected wide column is never read; projected wide read once per batch', async () => {
  const counter = { payload: 0, blob: 0 };
  const source = new BatchSource(makeSpecs(counter), 3);
  const plan: Plan = {
    filter: { col: 'status', op: 'gte', value: 10 },
    project: ['status', 'payload'],
  };
  const result = await execute(source, plan);
  assertEqual(counter.payload, 3, 'one physical load per batch with selections');
  assertEqual(counter.blob, 0, 'blob column never loaded');
  assert(!source.stats.loads.has('blob'), 'no stats entry for blob');
  const st = source.stats.loads.get('payload');
  assertEqual(st!.started, 3, 'started');
  assertEqual(st!.completed, 3, 'completed');
  assertSameResult(result, await runReference(makeSpecs(), plan));
});

test('filter + sort + project matches eager row-by-row reference value by value', async () => {
  const counter = { payload: 0, blob: 0 };
  const source = new BatchSource(makeSpecs(counter), 3);
  const plan: Plan = {
    filter: { col: 'status', op: 'gte', value: 10 },
    sort: { key: 'k', dir: 'desc' },
    project: ['status', 'k', 'payload'],
  };
  const result = await execute(source, plan);
  assertSameResult(result, await runReference(makeSpecs(), plan));
  assertEqual(counter.blob, 0, 'blob column never loaded');
});

test('filter on a wide column is rejected', async () => {
  const source = new BatchSource(makeSpecs(), 3);
  const it = source.batches();
  const b1 = (await it.next()).value as ColumnBatch;
  assertThrows(
    () => filterBatch(b1, { col: 'payload', op: 'eq', value: 'p1' }),
    UnknownColumnError,
    'predicates only run on narrow columns',
  );
});
