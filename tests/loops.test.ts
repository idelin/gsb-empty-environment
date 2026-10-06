import { Debugger, runProgram } from '../src/index.ts';
import { assert, assertDeepEqual, assertEqual, test } from './framework.ts';
import { findStmt, nodeIds } from './helpers.ts';

const LOOP = `let sum = 0;
let i = 1;
while (i <= 5) {
  sum = sum + i;
  i = i + 1;
}
print(sum);
`;

test('loop: full run and step-by-step run produce identical output and trace', () => {
  const full = runProgram(LOOP);
  assertEqual(full.error, null, 'full run error');
  assertDeepEqual(full.output, ['15'], 'full run output');

  const dbg = new Debugger(LOOP);
  let guard = 0;
  while (dbg.step() !== null) {
    if (++guard > 1000) throw new Error('stepping did not terminate');
  }
  assertDeepEqual(dbg.output, full.output, 'stepped output');
  assertDeepEqual(nodeIds(dbg.trace), nodeIds(full.trace), 'stepped trace node sequence');
});

test('loop: breakpoint fires before the node executes', () => {
  const target = findStmt(LOOP, 'sum = sum + i');
  const dbg = new Debugger(LOOP);
  dbg.addBreakpoint(target.id);

  const pause = dbg.continue();
  assert(pause !== null, 'expected a pause');
  assertEqual(pause.reason, 'breakpoint', 'pause reason');
  assertEqual(pause.nodeId, target.id, 'paused node id');
  assertEqual(dbg.getVariable('sum'), 0, 'sum must not be updated before the node executes');

  dbg.step();
  assertEqual(dbg.getVariable('sum'), 1, 'sum updated after stepping over the node');
});

test('loop: continue hits a multi-statement loop body breakpoint once per iteration', () => {
  const target = findStmt(LOOP, 'sum = sum + i');
  const dbg = new Debugger(LOOP);
  dbg.addBreakpoint(target.id);

  const sumsAtHit: unknown[] = [];
  const isAtHit: unknown[] = [];
  let pause = dbg.continue();
  while (pause !== null) {
    sumsAtHit.push(dbg.getVariable('sum'));
    isAtHit.push(dbg.getVariable('i'));
    pause = dbg.continue();
  }
  assertDeepEqual(sumsAtHit, [0, 1, 3, 6, 10], 'sum at each breakpoint hit');
  assertDeepEqual(isAtHit, [1, 2, 3, 4, 5], 'i at each breakpoint hit');
  assertDeepEqual(dbg.output, ['15'], 'output after running to completion');
});

const SINGLE_STMT_LOOP = `let i = 0;
while (i < 3) i = i + 1;
print(i);
`;

test('loop: one continue cannot immediately re-hit the same node', () => {
  const target = findStmt(SINGLE_STMT_LOOP, 'i = i + 1');
  const dbg = new Debugger(SINGLE_STMT_LOOP);
  const bp = dbg.addBreakpoint(target.id);

  const first = dbg.continue();
  assert(first !== null, 'expected first hit');
  assertEqual(dbg.getVariable('i'), 0, 'i at first hit');

  const second = dbg.continue();
  assertEqual(second, null, 'continue must not immediately re-hit the same node');
  assert(dbg.isFinished, 'program should have run to completion');
  assertDeepEqual(dbg.output, ['3'], 'loop still ran all iterations');

  const executions = dbg.trace.filter((t) => t.nodeId === target.id).length;
  assertEqual(executions, 3, 'trace records every execution of the suppressed node');
});

test('loop: step is not suppressed and stops at the same node again', () => {
  const target = findStmt(SINGLE_STMT_LOOP, 'i = i + 1');
  const dbg = new Debugger(SINGLE_STMT_LOOP);
  dbg.addBreakpoint(target.id);

  const first = dbg.continue();
  assert(first !== null, 'expected first hit');
  const next = dbg.step();
  assert(next !== null, 'expected step pause');
  assertEqual(next.nodeId, target.id, 'step stops at the same node on the next iteration');
  assertEqual(next.reason, 'step', 'step pause reason');
});
