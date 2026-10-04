import { test } from 'node:test';
import assert from 'node:assert/strict';

import { execute } from '../src/executor.ts';
import { referenceExecute } from '../src/reference.ts';
import { buildSelection } from '../src/operators.ts';
import { compare3 } from '../src/predicate.ts';
import { BatchSource } from '../src/source.ts';
import type { SourceSpec } from '../src/source.ts';
import {
  QueryAbortedError,
  ReleasedBatchError,
  StaleSelectionError,
} from '../src/errors.ts';
import type { QueryPlan, Value } from '../src/types.ts';

function makeSpec(overrides: Partial<SourceSpec> = {}): SourceSpec {
  return {
    narrowColumns: ['age', 'name'],
    wideColumns: ['bio', 'blob'],
    batches: [
      {
        dataVersion: 1,
        rowIds: [1, 2, 3, 4],
        rowVersions: [1, 1, 1, 1],
        columns: {
          age: [17, 34, null, 62],
          name: ['amy', 'bob', 'cy', 'di'],
          bio: ['a1', 'b1', 'c1', 'd1'],
          blob: ['A', 'B', 'C', 'D'],
        },
      },
      {
        dataVersion: 1,
        rowIds: [5, 6, 7, 8],
        rowVersions: [1, 1, 1, 1],
        columns: {
          age: [25, null, 41, 19],
          name: ['el', 'fi', 'cy', 'hu'],
          bio: ['e1', 'f1', 'g1', 'h1'],
          blob: ['E', 'F', 'G', 'H'],
        },
      },
    ],
    ...overrides,
  };
}

test('three-valued logic: null comparisons are unknown', () => {
  assert.equal(compare3(null, '=', 1), 'unknown');
  assert.equal(compare3(1, '<', null), 'unknown');
  assert.equal(compare3(null, '!=', null), 'unknown');
  assert.equal(compare3(2, '>', 1), 'true');
  assert.equal(compare3('a', '=', 'b'), 'false');
});

test('empty selection: no rows survive, no wide column is ever loaded', async () => {
  const source = new BatchSource(makeSpec());
  const plan: QueryPlan = {
    filter: { column: 'age', op: '>', value: 1000 },
    sort: { column: 'name' },
    project: ['name', 'bio'],
  };
  const result = await execute(source, plan);
  assert.deepEqual(result.rows, []);
  assert.deepEqual(result.stats, {});
  assert.deepEqual(result.rows, referenceExecute(source.spec, plan));
});

test('all-null filter column: every comparison is unknown, nothing is kept', async () => {
  const spec = makeSpec({
    narrowColumns: ['score'],
    batches: [
      {
        dataVersion: 3,
        rowIds: [10, 11, 12],
        rowVersions: [1, 1, 1],
        columns: {
          score: [null, null, null],
          bio: ['x', 'y', 'z'],
          blob: ['X', 'Y', 'Z'],
        },
      },
    ],
  });
  const source = new BatchSource(spec);
  const plan: QueryPlan = {
    filter: { column: 'score', op: '>=', value: 0 },
    project: ['bio'],
  };
  const result = await execute(source, plan);
  assert.deepEqual(result.rows, []);
  assert.equal(source.stats.loadCount('bio'), 0);
  assert.deepEqual(result.rows, referenceExecute(spec, plan));
});

test('cross-batch sort is stable: equal keys keep original input order', async () => {
  const spec = makeSpec({
    batches: [
      {
        dataVersion: 1,
        rowIds: [1, 2, 3],
        rowVersions: [1, 1, 1],
        columns: {
          age: [30, 20, 30],
          name: ['n1', 'n2', 'n3'],
          bio: ['b1', 'b2', 'b3'],
          blob: ['p', 'q', 'r'],
        },
      },
      {
        dataVersion: 1,
        rowIds: [4, 5, 6],
        rowVersions: [1, 1, 1],
        columns: {
          age: [20, 30, 10],
          name: ['n4', 'n5', 'n6'],
          bio: ['b4', 'b5', 'b6'],
          blob: ['s', 't', 'u'],
        },
      },
      {
        dataVersion: 1,
        rowIds: [7, 8],
        rowVersions: [1, 1],
        columns: {
          age: [30, 20],
          name: ['n7', 'n8'],
          bio: ['b7', 'b8'],
          blob: ['v', 'w'],
        },
      },
    ],
  });
  const source = new BatchSource(spec, { poolSize: 1 });
  const plan: QueryPlan = {
    filter: { column: 'age', op: '>=', value: 10 },
    sort: { column: 'age' },
    project: ['name', 'bio'],
  };
  const result = await execute(source, plan);
  // age=10: row 6; age=20: rows 2,4,8 (input order); age=30: rows 1,3,5,7.
  assert.deepEqual(
    result.rows.map((r) => r.rowId),
    [6, 2, 4, 8, 1, 3, 5, 7],
  );
  assert.deepEqual(result.rows, referenceExecute(spec, plan));
});

test('duplicate rowIds with different versions are distinct rows', async () => {
  const spec = makeSpec({
    batches: [
      {
        dataVersion: 1,
        rowIds: [7, 8],
        rowVersions: [1, 1],
        columns: {
          age: [40, 50],
          name: ['old7', 'n8'],
          bio: ['bio-old', 'bio-8'],
          blob: ['x', 'y'],
        },
      },
      {
        dataVersion: 2,
        rowIds: [7],
        rowVersions: [2],
        columns: {
          age: [60],
          name: ['new7'],
          bio: ['bio-new'],
          blob: ['z'],
        },
      },
    ],
  });
  const source = new BatchSource(spec);
  const plan: QueryPlan = {
    filter: { column: 'age', op: '>=', value: 40 },
    sort: { column: 'age' },
    project: ['name', 'bio'],
  };
  const result = await execute(source, plan);
  assert.deepEqual(
    result.rows.map((r) => [r.rowId, r.version]),
    [
      [7, 1],
      [8, 1],
      [7, 2],
    ],
  );
  assert.equal(result.rows[0].values.bio, 'bio-old');
  assert.equal(result.rows[2].values.bio, 'bio-new');
  assert.deepEqual(result.rows, referenceExecute(spec, plan));
});

test('cancellation: aborting stops the pipeline and no further loads happen', async () => {
  const events: string[] = [];
  const spec = makeSpec({
    batches: Array.from({ length: 6 }, (_, b) => ({
      dataVersion: 1,
      rowIds: [b * 10 + 1, b * 10 + 2],
      rowVersions: [1, 1],
      columns: {
        age: [20 + b, 30 + b],
        name: [`n${b}a`, `n${b}b`],
        bio: [`bio${b}a`, `bio${b}b`],
        blob: [`B${b}a`, `B${b}b`],
      },
    })),
  });
  const source = new BatchSource(spec, {
    poolSize: 2,
    loadLatencyMs: 5,
    onEvent: (e) => events.push(e.kind),
  });
  const controller = new AbortController();
  const plan: QueryPlan = {
    filter: { column: 'age', op: '>', value: 0 },
    sort: { column: 'age' },
    project: ['bio', 'name'],
  };
  const pending = execute(source, plan, controller.signal);
  // Let the scan finish and projection start, then abort mid-flight.
  setTimeout(() => controller.abort(), 12);
  await assert.rejects(pending, (err: unknown) => err instanceof QueryAbortedError);
  const loadsAfterAbort = source.stats.loadCount('bio');
  assert.ok(
    loadsAfterAbort < 6,
    `expected fewer than 6 bio loads after abort, got ${loadsAfterAbort}`,
  );
});

test('buffer reuse: stale selection and released views are rejected', async () => {
  const spec = makeSpec({
    batches: [
      {
        dataVersion: 1,
        rowIds: [1, 2],
        rowVersions: [1, 1],
        columns: { age: [10, 20], name: ['a', 'b'], bio: ['ba', 'bb'], blob: ['x', 'y'] },
      },
      {
        dataVersion: 9,
        rowIds: [3, 4],
        rowVersions: [1, 1],
        columns: { age: [30, 40], name: ['c', 'd'], bio: ['bc', 'bd'], blob: ['z', 'w'] },
      },
    ],
  });
  const source = new BatchSource(spec, { poolSize: 1 });

  const first = await source.acquire();
  assert.ok(first !== null);
  const selection = buildSelection(first, { column: 'age', op: '>', value: 0 });
  assert.deepEqual([...selection.resolve(first)], [0, 1]);
  const ageView = first.column('age');
  assert.equal(ageView.value(0), 10);
  first.release();

  // Releasing the batch invalidates views derived from it.
  assert.throws(() => ageView.value(0), ReleasedBatchError);
  assert.throws(() => first.column('age'), ReleasedBatchError);
  await assert.rejects(source.loadColumn(first, 'bio', null), ReleasedBatchError);

  // The single slot is reused for the next batch (generation bumped).
  const second = await source.acquire();
  assert.ok(second !== null);
  assert.equal(second.slotId, first.slotId);
  assert.equal(second.generation, first.generation + 1);
  assert.equal(second.dataVersion, 9);

  // Row numbers from the previous occupant must not map onto the new data.
  assert.throws(() => selection.resolve(second), StaleSelectionError);
  second.release();
});

test('unprojected wide columns are never read; projected ones load once per needed batch', async () => {
  const spec = makeSpec({
    wideColumns: ['bio', 'blob', 'payload'],
    batches: [
      {
        dataVersion: 1,
        rowIds: [1, 2, 3],
        rowVersions: [1, 1, 1],
        columns: {
          age: [10, 50, 70],
          name: ['a', 'b', 'c'],
          bio: ['ba', 'bb', 'bc'],
          blob: ['xa', 'xb', 'xc'],
          payload: ['pa', 'pb', 'pc'],
        },
      },
      {
        dataVersion: 1,
        rowIds: [4, 5],
        rowVersions: [1, 1],
        columns: {
          age: [5, 60],
          name: ['d', 'e'],
          bio: ['bd', 'be'],
          blob: ['xd', 'xe'],
          payload: ['pd', 'pe'],
        },
      },
    ],
  });
  const source = new BatchSource(spec);
  const plan: QueryPlan = {
    filter: { column: 'age', op: '>=', value: 50 },
    project: ['name', 'bio'],
  };
  const result = await execute(source, plan);
  assert.deepEqual(
    result.rows.map((r) => r.values.bio),
    ['bb', 'bc', 'be'],
  );
  assert.equal(source.stats.loadCount('bio'), 2, 'bio loaded once per batch with survivors');
  assert.equal(source.stats.rowsLoaded('bio'), 3, 'only surviving rows are gathered');
  assert.equal(source.stats.loadCount('blob'), 0);
  assert.equal(source.stats.loadCount('payload'), 0);
  assert.deepEqual(result.rows, referenceExecute(spec, plan));
});

test('pipeline matches eager row-by-row reference value by value', async () => {
  // Deterministic pseudo-random dataset with nulls and duplicate keys.
  let seed = 42;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  const maybe = <T extends Value>(v: T): Value => (rand() < 0.25 ? null : v);
  const batches = Array.from({ length: 5 }, (_, b) => {
    const n = 3 + (b % 3);
    const rowIds: number[] = [];
    const rowVersions: number[] = [];
    const age: Value[] = [];
    const name: Value[] = [];
    const score: Value[] = [];
    const bio: Value[] = [];
    const blob: Value[] = [];
    for (let i = 0; i < n; i++) {
      rowIds.push(b * 2 + (i % 3)); // deliberate duplicate rowIds across batches
      rowVersions.push(1 + ((b + i) % 2));
      age.push(maybe(Math.floor(rand() * 80)));
      name.push(`n${b}_${i}`);
      score.push(maybe(Math.floor(rand() * 5)));
      bio.push(maybe(`bio${b}_${i}`));
      blob.push(`blob${b}_${i}`);
    }
    return {
      dataVersion: 1 + (b % 2),
      rowIds,
      rowVersions,
      columns: { age, name, score, bio, blob },
    };
  });
  const spec: SourceSpec = {
    narrowColumns: ['age', 'name'],
    wideColumns: ['score', 'bio', 'blob'],
    batches,
  };
  const plan: QueryPlan = {
    filter: { column: 'age', op: '>=', value: 20 },
    sort: { column: 'score', direction: 'desc' },
    project: ['name', 'score', 'bio'],
  };
  const source = new BatchSource(spec, { poolSize: 2 });
  const result = await execute(source, plan);
  const expected = referenceExecute(spec, plan);
  assert.deepEqual(result.rows, expected);
  assert.ok(result.rows.length > 0, 'dataset should produce rows');
  // 'blob' is wide and unprojected: it must never be loaded.
  assert.equal(source.stats.loadCount('blob'), 0);
  // 'score' is the wide sort key and is also projected: loaded once per
  // non-empty batch during the scan (sort keys) and once more per batch with
  // survivors during projection.
  const nonEmptyBatches = batches.filter((b) =>
    b.columns.age.some((a) => a !== null && (a as number) >= 20),
  ).length;
  assert.equal(source.stats.loadCount('score'), 2 * nonEmptyBatches);
});
