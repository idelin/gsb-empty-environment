import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SchedulingSolver } from '../src/solver.ts';

const DAY = 480; // one workday = 480 working minutes

/**
 * Acceptance scenario:
 *  - A must finish before B starts        (A before B)
 *  - B and C partially overlap            (B overlaps C)
 *  - C must start before project day 5    (C.start < 5 * DAY)
 *  - the project must finish by day 9     (C.end <= 9 * DAY)
 *  - D (1 day) only needs to finish before C starts (slack task)
 */
function buildProject() {
  const solver = new SchedulingSolver({ epsilon: 1 });
  const A = solver.addTask('A', 2 * DAY);
  const B = solver.addTask('B', 3 * DAY);
  const C = solver.addTask('C', 4 * DAY);
  const D = solver.addTask('D', 1 * DAY);
  solver.constrain(A, B, 'before');
  solver.constrain(B, C, 'overlaps');
  solver.constrain(D, C, 'before');
  solver.require(C, 'start', '<', 5 * DAY);
  solver.require(C, 'end', '<=', 9 * DAY);
  return { solver, A, B, C, D };
}

test('acceptance scenario is consistent', () => {
  const { solver } = buildProject();
  assert.equal(solver.solve().consistent, true);
});

test('temporal propagation derives A before C', () => {
  const { solver, A, C } = buildProject();
  const result = solver.solve();
  assert.deepEqual(result.relationBetween(A, C), ['b']);
  assert.equal(result.relationLabel(A, C), '{b}');
});

test('task A latest finish is bounded by the day-5 deadline chain', () => {
  const { solver } = buildProject();
  const result = solver.solve();
  const a = result.windows[0];
  // A.end < B.start < C.start < 5*DAY  =>  A.end <= 5*DAY - 3*epsilon
  assert.equal(a.earliestStart, 0);
  assert.equal(a.earliestFinish, 2 * DAY);
  assert.equal(a.latestFinish, 5 * DAY - 3);
  assert.equal(a.latestStart, 5 * DAY - 3 - 2 * DAY);
});

test('all task windows match hand-computed propagation', () => {
  const { solver } = buildProject();
  const [a, b, c, d] = solver.solve().windows;
  assert.deepEqual(
    [b.earliestStart, b.earliestFinish, b.latestStart, b.latestFinish],
    [2 * DAY + 1, 5 * DAY + 1, 5 * DAY - 2, 8 * DAY - 2],
  );
  assert.deepEqual(
    [c.earliestStart, c.earliestFinish, c.latestStart, c.latestFinish],
    [2 * DAY + 2, 6 * DAY + 2, 5 * DAY - 1, 9 * DAY - 1],
  );
  assert.deepEqual(
    [d.earliestStart, d.earliestFinish, d.latestStart, d.latestFinish],
    [0, DAY, 5 * DAY - 2 - DAY, 5 * DAY - 2],
  );
  assert.equal(a.float, 3 * DAY - 3);
  assert.equal(d.float, 4 * DAY - 2);
});

test('critical path is A-B-C; slack task D is not critical', () => {
  const { solver } = buildProject();
  const result = solver.solve();
  assert.deepEqual(result.criticalTasks, ['A', 'B', 'C']);
  assert.deepEqual(result.criticalWindow, { start: 0, end: 9 * DAY - 1 });
  assert.deepEqual(result.projectWindow, {
    earliestStart: 0,
    latestFinish: 9 * DAY - 1,
  });
});

test('disjunctive constraint {b,m} propagates quantitatively', () => {
  const solver = new SchedulingSolver({ epsilon: 1 });
  const A = solver.addTask('A', DAY);
  const B = solver.addTask('B', DAY);
  solver.constrain(A, B, '{b,m}');
  solver.require(B, 'end', '<=', 1000);
  const result = solver.solve();
  assert.equal(result.consistent, true);
  const [a, b] = result.windows;
  assert.equal(a.latestFinish, 520); // A.end <= B.start <= 1000 - 480
  assert.equal(a.latestStart, 40);
  assert.equal(b.earliestStart, DAY);
});

test('inconsistent point constraint is reported', () => {
  const { solver, A } = buildProject();
  // A must start after day 10, but the deadline chain forces A.start <= 1437.
  solver.require(A, 'start', '>', 10 * DAY);
  assert.equal(solver.solve().consistent, false);
});

test('qualitative contradiction is reported', () => {
  const solver = new SchedulingSolver();
  const A = solver.addTask('A', 10);
  const B = solver.addTask('B', 10);
  solver.constrain(A, B, 'before');
  solver.constrain(B, A, 'before');
  assert.equal(solver.solve().consistent, false);
});

test('duration ranges create flexible windows', () => {
  const solver = new SchedulingSolver({ epsilon: 1 });
  const A = solver.addTask('A', { min: 10, max: 20 });
  solver.require(A, 'end', '<=', 100);
  const result = solver.solve();
  assert.equal(result.consistent, true);
  const [a] = result.windows;
  assert.equal(a.earliestFinish, 10);
  assert.equal(a.latestFinish, 100);
  assert.equal(a.latestStart, 90); // 100 - minDuration
});
