import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import {
  FileCheckpointStore,
  MemoryCheckpointStore,
  defineStep,
  failAt,
  transformStream,
  type Step,
} from '../src/index.ts';
import type { JsonObject } from '../src/json.ts';

const validate: Step = defineStep('validate', (rec) => {
  if (typeof rec.name !== 'string') failAt(['name'], 'must be a string');
  if (!Array.isArray(rec.items)) failAt(['items'], 'must be an array');
  rec.items.forEach((item, i) => {
    if (typeof item !== 'object' || item === null || Array.isArray(item)) {
      failAt(['items', i], 'must be an object');
    }
    const qty = (item as JsonObject).qty;
    if (typeof qty !== 'number') failAt(['items', i, 'qty'], 'must be a number');
  });
  return rec;
});

const normalize: Step = defineStep('normalize', (rec) => {
  const items = (rec.items as JsonObject[]).map((item) => ({
    sku: item.sku,
    qty: item.qty,
  }));
  const total = items.reduce((sum, item) => sum + (item.qty as number), 0);
  return { ...rec, name: (rec.name as string).toUpperCase(), items, total };
});

const steps = [validate, normalize];

const good = (id: string, qtys: number[]): JsonObject => ({
  id,
  name: `order-${id}`,
  items: qtys.map((qty, i) => ({ sku: `s${i}`, qty })),
});

const records: JsonObject[] = [
  good('r1', [1, 2]),
  { id: 'r2', name: 'order-r2', items: [{ sku: 's0', qty: 1 }, { sku: 's1', qty: 'oops' }] },
  good('r3', [3]),
  { id: 'r4', items: [] },
  good('r5', [4, 5, 6]),
];

async function* crashing(input: JsonObject[], crashAfter: number): AsyncGenerator<JsonObject> {
  for (let i = 0; i < input.length; i += 1) {
    if (i === crashAfter) throw new Error('boom: simulated crash');
    yield input[i];
  }
}

describe('isolated mode', () => {
  it('emits good records in input order and keeps going after bad ones', async () => {
    const emittedIds: string[] = [];
    const result = await transformStream({
      records,
      steps,
      mode: 'isolated',
      checkpoint: new MemoryCheckpointStore(),
      onEmit: (rec) => emittedIds.push(rec.id as string),
    });

    assert.deepEqual(emittedIds, ['r1', 'r3', 'r5']);
    assert.deepEqual(result.emitted.map((r) => r.id), ['r1', 'r3', 'r5']);
    assert.deepEqual(result.emitted.map((r) => r.total), [3, 3, 15]);
    assert.equal(result.errors.length, 2);
    assert.deepEqual(result.stats, { seen: 5, emitted: 3, failed: 2, skipped: 0 });
  });

  it('locates every error by record id, step, and json path', async () => {
    const result = await transformStream({
      records,
      steps,
      mode: 'isolated',
      checkpoint: new MemoryCheckpointStore(),
    });
    const [e2, e4] = result.errors;
    assert.equal(e2.recordId, 'r2');
    assert.equal(e2.step, 'validate');
    assert.equal(e2.pathString, '$.items[1].qty');
    assert.equal(e4.recordId, 'r4');
    assert.equal(e4.step, 'validate');
    assert.equal(e4.pathString, '$.name');
    assert.match(e2.message, /record "r2".*step "validate".*\$\.items\[1\]\.qty/);
  });

  it('does not re-emit or re-count records on rerun with the same checkpoint', async () => {
    const store = new MemoryCheckpointStore();
    const first = await transformStream({ records, steps, mode: 'isolated', checkpoint: store });
    const second = await transformStream({ records, steps, mode: 'isolated', checkpoint: store });

    assert.equal(first.stats.emitted, 3);
    assert.equal(second.stats.emitted, 0);
    assert.equal(second.stats.failed, 0);
    assert.equal(second.stats.skipped, 5);
    assert.deepEqual(store.counts(), { emitted: 3, failed: 2, staged: 0 });
  });
});

describe('atomic mode', () => {
  it('publishes nothing when any record fails', async () => {
    const emitted: string[] = [];
    const result = await transformStream({
      records,
      steps,
      mode: 'atomic',
      checkpoint: new MemoryCheckpointStore(),
      onEmit: (rec) => emitted.push(rec.id as string),
    });

    assert.equal(result.committed, false);
    assert.deepEqual(result.emitted, []);
    assert.deepEqual(emitted, []);
    assert.equal(result.errors.length, 1);
    assert.equal(result.errors[0].recordId, 'r2');
    assert.equal(result.errors[0].pathString, '$.items[1].qty');
  });

  it('publishes the whole batch in order when everything passes', async () => {
    const all = [good('a', [1]), good('b', [2]), good('c', [3])];
    const result = await transformStream({
      records: all,
      steps,
      mode: 'atomic',
      checkpoint: new MemoryCheckpointStore(),
    });

    assert.equal(result.committed, true);
    assert.deepEqual(result.emitted.map((r) => r.id), ['a', 'b', 'c']);
    assert.deepEqual(result.emitted.map((r) => r.name), ['ORDER-A', 'ORDER-B', 'ORDER-C']);
  });
});

describe('crash recovery with a file checkpoint', () => {
  const dir = () => mkdtempSync(join(tmpdir(), 'stream-transform-'));

  it('isolated: resumes without duplicate output and without double-counting failures', async () => {
    const file = join(dir(), 'checkpoint.jsonl');
    const emittedIds: string[] = [];
    const onEmit = (rec: JsonObject): void => {
      emittedIds.push(rec.id as string);
    };

    await assert.rejects(
      transformStream({
        records: crashing(records, 3),
        steps,
        mode: 'isolated',
        checkpoint: new FileCheckpointStore(file),
        onEmit,
      }),
      /boom/,
    );

    const resumed = await transformStream({
      records,
      steps,
      mode: 'isolated',
      checkpoint: new FileCheckpointStore(file),
      onEmit,
    });

    assert.deepEqual(emittedIds, ['r1', 'r3', 'r5']);
    assert.equal(resumed.stats.skipped, 3);
    assert.equal(resumed.stats.failed, 1);
    const counts = new FileCheckpointStore(file).counts();
    assert.deepEqual(counts, { emitted: 3, failed: 2, staged: 0 });
  });

  it('atomic: resumes a crashed batch and publishes it exactly once', async () => {
    const file = join(dir(), 'checkpoint.jsonl');
    const all = [good('a', [1]), good('b', [2]), good('c', [3]), good('d', [4])];
    let validateCalls = 0;
    const countingSteps: Step[] = [
      defineStep('validate', (rec) => {
        validateCalls += 1;
        return validate.apply(rec, { recordId: String(rec.id), recordIndex: 0 });
      }),
      normalize,
    ];
    const emittedIds: string[] = [];
    const onEmit = (rec: JsonObject): void => {
      emittedIds.push(rec.id as string);
    };

    await assert.rejects(
      transformStream({
        records: crashing(all, 2),
        steps: countingSteps,
        mode: 'atomic',
        checkpoint: new FileCheckpointStore(file),
        onEmit,
      }),
      /boom/,
    );
    assert.equal(emittedIds.length, 0);

    const resumed = await transformStream({
      records: all,
      steps: countingSteps,
      mode: 'atomic',
      checkpoint: new FileCheckpointStore(file),
      onEmit,
    });
    assert.equal(resumed.committed, true);
    assert.deepEqual(emittedIds, ['a', 'b', 'c', 'd']);
    assert.equal(validateCalls, 4);

    const again = await transformStream({
      records: all,
      steps: countingSteps,
      mode: 'atomic',
      checkpoint: new FileCheckpointStore(file),
      onEmit,
    });
    assert.equal(again.committed, false);
    assert.deepEqual(emittedIds, ['a', 'b', 'c', 'd']);
    assert.equal(validateCalls, 4);
  });
});
