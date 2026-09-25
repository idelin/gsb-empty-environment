import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compilePath } from '../src/plan.ts';
import { PlanCursor, executePlan } from '../src/vm.ts';
import { PathError } from '../src/errors.ts';

const doc = {
  items: Array.from({ length: 50 }, (_, i) => ({ n: i, tag: i % 2 === 0 ? 'even' : 'odd' })),
  deep: { a: { b: { c: 1 } } },
};

test('budget exceeded raises PathError with the user path', () => {
  assert.throws(
    () => executePlan(compilePath('items[*].n'), doc, 5),
    (e: unknown) =>
      e instanceof PathError &&
      e.code === 'BUDGET_EXCEEDED' &&
      e.userPath === 'items[*].n',
  );
});

test('recursive descent, filtering and output all consume budget', () => {
  const descend = new PlanCursor(compilePath('..n'), doc, 100_000);
  descend.run();
  const filtered = new PlanCursor(compilePath('items[?(@.n >= 0)]'), doc, 100_000);
  filtered.run();
  const emitOnly = new PlanCursor(compilePath('deep.a.b.c'), doc, 100_000);
  emitOnly.run();
  assert.ok(descend.stepsUsed > 50, `descend steps ${descend.stepsUsed}`);
  assert.ok(filtered.stepsUsed > 50, `filter steps ${filtered.stepsUsed}`);
  assert.ok(emitOnly.stepsUsed >= 5, `emit steps ${emitOnly.stepsUsed}`);
});

test('cursor is pausable and resumable with identical results', () => {
  const source = 'items[?(@.n >= 40)].tag';
  const oneShot = executePlan(compilePath(source), doc, 100_000);

  const cursor = new PlanCursor(compilePath(source), doc, 100_000);
  const collected = [];
  while (!cursor.run(3)) {
    collected.push(...cursor.takeMatches()); // pause every 3 micro-steps
  }
  collected.push(...cursor.takeMatches());
  assert.deepEqual(collected, oneShot);
  assert.ok(cursor.done);
});

test('zero budget fails immediately on first step', () => {
  assert.throws(
    () => executePlan(compilePath('deep'), doc, 0),
    (e: unknown) => e instanceof PathError && e.code === 'BUDGET_EXCEEDED',
  );
});
