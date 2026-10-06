import { Debugger, runProgram } from '../src/index.ts';
import { assert, assertDeepEqual, assertEqual, test } from './framework.ts';
import { findStmt, nodeIds } from './helpers.ts';

const FACT = `fn fact(n) {
  if (n <= 1) {
    return 1;
  }
  return n * fact(n - 1);
}
print(fact(5));
`;

test('recursion: full run and stepped run agree on output and trace', () => {
  const full = runProgram(FACT);
  assertEqual(full.error, null, 'full run error');
  assertDeepEqual(full.output, ['120'], 'factorial output');

  const dbg = new Debugger(FACT);
  let guard = 0;
  while (dbg.step() !== null) {
    if (++guard > 5000) throw new Error('stepping did not terminate');
  }
  assertDeepEqual(dbg.output, full.output, 'stepped output');
  assertDeepEqual(nodeIds(dbg.trace), nodeIds(full.trace), 'stepped trace node sequence');
});

test('recursion: trace depth reflects the call stack', () => {
  const full = runProgram(FACT);
  const maxDepth = Math.max(...full.trace.map((t) => t.depth));
  assertEqual(maxDepth, 5, 'fact(5) bottoms out at depth 5');

  const baseCase = findStmt(FACT, 'return 1');
  const baseHits = full.trace.filter((t) => t.nodeId === baseCase.id);
  assertEqual(baseHits.length, 1, 'base case executes exactly once');
  assertEqual(baseHits[0].depth, 5, 'base case executes at maximum depth');
});

test('recursion: conditional breakpoint hits only when the condition holds', () => {
  const ifStmt = findStmt(FACT, 'if (n <= 1)');
  const dbg = new Debugger(FACT);
  dbg.addBreakpoint(ifStmt.id, 'n == 2');

  const pause = dbg.continue();
  assert(pause !== null, 'expected a conditional hit');
  assertEqual(dbg.getVariable('n'), 2, 'n at conditional hit');
  assertEqual(pause.depth, 4, 'call depth at conditional hit');

  const again = dbg.continue();
  assertEqual(again, null, 'n == 2 happens exactly once');
  assertDeepEqual(dbg.output, ['120'], 'conditional breakpoints do not change the result');
  assertDeepEqual(dbg.conditionErrors, [], 'no condition errors for pure conditions');
});

test('recursion: conditional breakpoint hit counts for several conditions', () => {
  const ifStmt = findStmt(FACT, 'if (n <= 1)');

  const countHits = (condition: string): number => {
    const dbg = new Debugger(FACT);
    dbg.addBreakpoint(ifStmt.id, condition);
    let hits = 0;
    while (dbg.continue() !== null) hits++;
    assertDeepEqual(dbg.output, ['120'], `output with condition '${condition}'`);
    assertDeepEqual(dbg.conditionErrors, [], `condition errors for '${condition}'`);
    return hits;
  };

  assertEqual(countHits('n <= 2'), 2, 'hits for n <= 2');
  assertEqual(countHits('n > 3'), 2, 'hits for n > 3');
  assertEqual(countHits('n == 1 && true'), 1, 'hits for n == 1 && true');
  assertEqual(countHits('false'), 0, 'hits for constant false');
});
