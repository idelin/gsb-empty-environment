/**
 * Runnable demo: `npm run sample`
 *
 * Scenario: a stream of order records flows through a two-step pipeline
 * (validate -> normalize). Record r2 has a bad qty, r4 has no name.
 *
 * 1. Isolated mode: bad records are rejected with precise locations, good
 *    records keep flowing in order.
 * 2. Atomic mode: the same stream publishes nothing at all.
 * 3. Crash + recovery: the stream dies mid-batch; a new process (fresh
 *    checkpoint store over the same file) resumes without re-emitting or
 *    re-counting anything.
 */
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
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
  (rec.items as JsonObject[]).forEach((item, i) => {
    if (typeof item.qty !== 'number') failAt(['items', i, 'qty'], 'must be a number');
  });
  return rec;
});

const normalize: Step = defineStep('normalize', (rec) => {
  const items = rec.items as JsonObject[];
  const total = items.reduce((sum, item) => sum + (item.qty as number), 0);
  return { ...rec, name: (rec.name as string).toUpperCase(), total };
});

const steps = [validate, normalize];

const records: JsonObject[] = [
  { id: 'r1', name: 'apples', items: [{ sku: 'a', qty: 2 }, { sku: 'b', qty: 1 }] },
  { id: 'r2', name: 'broken', items: [{ sku: 'a', qty: 1 }, { sku: 'b', qty: 'oops' }] },
  { id: 'r3', name: 'cherries', items: [{ sku: 'c', qty: 5 }] },
  { id: 'r4', items: [] },
  { id: 'r5', name: 'dates', items: [{ sku: 'd', qty: 4 }] },
];

const show = (rec: JsonObject): string =>
  `  -> emitted ${JSON.stringify({ id: rec.id, name: rec.name, total: rec.total })}`;

console.log('=== 1. isolated mode: one bad record never blocks the good ones ===');
const isolated = await transformStream({
  records,
  steps,
  mode: 'isolated',
  checkpoint: new MemoryCheckpointStore(),
  onEmit: (rec) => console.log(show(rec)),
});
for (const err of isolated.errors) {
  console.log(`  xx rejected ${JSON.stringify(err.toJSON())}`);
}
console.log(`  stats: ${JSON.stringify(isolated.stats)}`);

console.log('\n=== 2. atomic mode: any failure publishes nothing ===');
const atomic = await transformStream({
  records,
  steps,
  mode: 'atomic',
  checkpoint: new MemoryCheckpointStore(),
  onEmit: (rec) => console.log(show(rec)),
});
console.log(`  committed: ${atomic.committed}, emitted: ${atomic.emitted.length}`);
console.log(`  first error: ${atomic.errors[0]?.message}`);

console.log('\n=== 3. crash mid-stream, then recover from the file checkpoint ===');
const file = join(mkdtempSync(join(tmpdir(), 'sample-')), 'checkpoint.jsonl');

async function* crashing(): AsyncGenerator<JsonObject> {
  for (let i = 0; i < records.length; i += 1) {
    if (i === 3) throw new Error('simulated crash (process killed)');
    yield records[i];
  }
}

const sink: string[] = [];
try {
  await transformStream({
    records: crashing(),
    steps,
    mode: 'isolated',
    checkpoint: new FileCheckpointStore(file),
    onEmit: (rec) => sink.push(String(rec.id)),
  });
} catch (err) {
  console.log(`  !! crashed: ${(err as Error).message}; emitted so far: [${sink}]`);
}

const recovered = await transformStream({
  records,
  steps,
  mode: 'isolated',
  checkpoint: new FileCheckpointStore(file),
  onEmit: (rec) => sink.push(String(rec.id)),
});
console.log(`  resumed run stats: ${JSON.stringify(recovered.stats)}`);
console.log(`  total emitted across both runs (no duplicates): [${sink}]`);
console.log(`  durable counts: ${JSON.stringify(new FileCheckpointStore(file).counts())}`);
