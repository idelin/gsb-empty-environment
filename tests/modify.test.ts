import { Debugger, runProgram } from '../src/index.ts';
import { assert, assertDeepEqual, assertEqual, test } from './framework.ts';
import { findStmt, nodeIds } from './helpers.ts';

const ACCUM = `let total = 0;
let i = 1;
while (i <= 3) {
  total = total + i;
  i = i + 1;
}
print(total);
`;

test('modify: changing a variable while paused alters the final result', () => {
  const full = runProgram(ACCUM);
  assertDeepEqual(full.output, ['6'], 'unmodified output');

  const target = findStmt(ACCUM, 'total = total + i');
  const dbg = new Debugger(ACCUM);
  dbg.addBreakpoint(target.id);

  let pause = dbg.continue();
  assert(pause !== null, 'first hit');
  assertEqual(dbg.getVariable('total'), 0, 'total before first accumulation');
  assertEqual(dbg.getVariable('i'), 1, 'i at first hit');
  dbg.setVariable('total', 100);
  assertEqual(dbg.getVariable('total'), 100, 'total after modification');

  pause = dbg.continue();
  assert(pause !== null, 'second hit');
  assertEqual(dbg.getVariable('total'), 101, 'modified total flows into the next iteration');

  pause = dbg.continue();
  assert(pause !== null, 'third hit');
  assertEqual(dbg.getVariable('total'), 103, 'total at third hit');

  pause = dbg.continue();
  assertEqual(pause, null, 'runs to completion');
  assertDeepEqual(dbg.output, ['106'], 'final output reflects the paused modification');

  assertDeepEqual(
    nodeIds(dbg.trace),
    nodeIds(full.trace),
    'modifying a value does not change the executed statement sequence here',
  );
});

test('modify: changing the loop variable while paused changes control flow', () => {
  const target = findStmt(ACCUM, 'total = total + i');
  const dbg = new Debugger(ACCUM);
  dbg.addBreakpoint(target.id);

  const pause = dbg.continue();
  assert(pause !== null, 'first hit');
  dbg.setVariable('i', 10);

  const rest = dbg.continue();
  assertEqual(rest, null, 'loop exits after the modified iteration');
  assertDeepEqual(dbg.output, ['10'], 'total accumulated the injected value of i');
});

test('modify: setVariable defines a new variable visible to later code', () => {
  const src = `let a = 1;
print(a + extra);
`;
  const target = findStmt(src, 'print(a + extra)');
  const dbg = new Debugger(src);
  dbg.addBreakpoint(target.id);
  dbg.continue();
  dbg.setVariable('extra', 41);
  dbg.continue();
  assertDeepEqual(dbg.output, ['42'], 'newly defined variable is visible after resume');
});

test('modify: setVariable while not paused throws', () => {
  const dbg = new Debugger(ACCUM);
  let threw = false;
  try {
    dbg.setVariable('total', 5);
  } catch {
    threw = true;
  }
  assert(threw, 'setVariable before any pause must throw');

  dbg.runToEnd();
  threw = false;
  try {
    dbg.setVariable('total', 5);
  } catch {
    threw = true;
  }
  assert(threw, 'setVariable after completion must throw');
});
