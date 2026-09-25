import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from '../src/index.ts';
import { BudgetExceededError } from '../src/errors.ts';

function wideDoc(width: number): Record<string, unknown> {
  const items: Record<string, unknown>[] = [];
  for (let i = 0; i < width; i++) items.push({ id: i, nest: { id: i } });
  return { items };
}

test('total step budget is bounded; exceeding it raises BudgetExceededError', () => {
  const doc = wideDoc(500);
  const plan = compile('$..id');
  assert.throws(
    () => plan.run(doc, { maxTotalSteps: 50 }),
    (err: unknown) => err instanceof BudgetExceededError
      && /step budget exhausted/.test((err as Error).message)
      && (err as InstanceType<typeof BudgetExceededError>).expression === '$..id',
  );
  assert.doesNotThrow(() => plan.run(doc, { maxTotalSteps: 100_000 }));
});

test('plan is pausable and resumable; sliced execution equals one-shot', () => {
  const doc = wideDoc(50);
  const plan = compile('$.items[*].nest.id');
  const expected = plan.run(doc).map((m) => m.value);

  const execution = plan.start(doc);
  const collected: unknown[] = [];
  let seen = 0;
  let done = false;
  for (let guard = 0; guard < 100_000 && !done; guard++) {
    const r = execution.resume(3); // grant 3 steps at a time
    done = r.done;
    assert.ok(r.totalSteps >= seen);
    seen = r.totalSteps;
    collected.length = 0;
    collected.push(...r.matches.map((m) => m.value));
  }
  assert.ok(done, 'execution must eventually finish');
  assert.deepEqual(collected, expected);
});

test('resume after completion is a no-op returning final results', () => {
  const plan = compile('$.a');
  const execution = plan.start({ a: 1 });
  const first = execution.resume(1000);
  assert.ok(first.done);
  const second = execution.resume(1000);
  assert.ok(second.done);
  assert.equal(second.matches.length, 1);
  assert.equal(second.totalSteps, first.totalSteps);
});

test('output emission consumes budget', () => {
  const doc = wideDoc(200);
  const plan = compile('$.items[*].id');
  const cheap = plan.start(doc, { maxTotalSteps: 10_000 });
  const r = cheap.resume(10_000);
  assert.ok(r.done);
  assert.ok(r.totalSteps > 200, 'enumeration plus output must cost steps');
});

test('filter evaluation consumes budget per node', () => {
  const doc = wideDoc(100);
  const simple = compile('$.items[?(@.id >= 0)]').run(doc, { maxTotalSteps: 100_000 });
  const complex = compile('$.items[?(@.id >= 0 && !(@.id == 999999) || @.id == 5)]');
  assert.equal(simple.length, 100);
  assert.throws(() => complex.run(doc, { maxTotalSteps: 100 }), BudgetExceededError);
});
