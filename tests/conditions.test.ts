import { Debugger, ParseError, runProgram } from '../src/index.ts';
import { assert, assertDeepEqual, assertEqual, test } from './framework.ts';
import { findStmt } from './helpers.ts';

const PROGRAM = `let x = 0;
let i = 0;
while (i < 4) {
  x = x + 1;
  i = i + 1;
}
print(x);
`;

test('conditions: assignment in a condition is a side-effect error and does not mutate state', () => {
  const target = findStmt(PROGRAM, 'x = x + 1');
  const dbg = new Debugger(PROGRAM);
  dbg.addBreakpoint(target.id, 'x = 99');
  dbg.runToEnd();

  assertEqual(dbg.conditionErrors.length, 4, 'one condition error per encounter');
  for (const err of dbg.conditionErrors) {
    assertEqual(err.kind, 'side-effect', 'error kind');
    assertEqual(err.nodeId, target.id, 'error node id');
    assertEqual(err.condition, 'x = 99', 'error condition text');
  }
  assertDeepEqual(dbg.output, ['4'], 'program output is unaffected');
  assertEqual(dbg.getVariable('x'), 4, 'x was never assigned 99 by the condition');
  assertEqual(dbg.error, null, 'no runtime error in the program itself');
});

test('conditions: function calls in a condition are side-effect errors and print nothing', () => {
  const target = findStmt(PROGRAM, 'x = x + 1');
  const dbg = new Debugger(PROGRAM);
  dbg.addBreakpoint(target.id, 'print(x)');
  dbg.runToEnd();

  assertEqual(dbg.conditionErrors.length, 4, 'one condition error per encounter');
  for (const err of dbg.conditionErrors) assertEqual(err.kind, 'side-effect', 'error kind');
  assertDeepEqual(dbg.output, ['4'], 'condition evaluation printed nothing');
});

test('conditions: evaluation failures are listed separately from side-effect errors', () => {
  const target = findStmt(PROGRAM, 'x = x + 1');
  const dbg = new Debugger(PROGRAM);
  dbg.addBreakpoint(target.id, 'missing > 1');
  dbg.runToEnd();

  assertEqual(dbg.conditionErrors.length, 4, 'one condition error per encounter');
  for (const err of dbg.conditionErrors) {
    assertEqual(err.kind, 'evaluation', 'error kind');
    assert(err.message.includes("undefined variable 'missing'"), 'error message mentions the variable');
  }
  assertDeepEqual(dbg.output, ['4'], 'program output is unaffected');
  assertEqual(dbg.error, null, 'condition errors do not become runtime errors');
});

test('conditions: division by zero in a condition is an evaluation error', () => {
  const target = findStmt(PROGRAM, 'x = x + 1');
  const dbg = new Debugger(PROGRAM);
  dbg.addBreakpoint(target.id, 'x / 0 > 1');
  dbg.runToEnd();

  assertEqual(dbg.conditionErrors.length, 4, 'one condition error per encounter');
  for (const err of dbg.conditionErrors) {
    assertEqual(err.kind, 'evaluation', 'error kind');
    assert(err.message.includes('division by zero'), 'error message');
  }
});

test('conditions: a valid pure condition gates breakpoint hits', () => {
  const target = findStmt(PROGRAM, 'x = x + 1');
  const dbg = new Debugger(PROGRAM);
  dbg.addBreakpoint(target.id, 'x >= 2');

  const xs: unknown[] = [];
  let pause = dbg.continue();
  while (pause !== null) {
    xs.push(dbg.getVariable('x'));
    pause = dbg.continue();
  }
  assertDeepEqual(xs, [2, 3], 'hits only when x >= 2 before the node executes');
  assertDeepEqual(dbg.conditionErrors, [], 'no condition errors');
  assertDeepEqual(dbg.output, ['4'], 'output unaffected');
});

test('conditions: an unparseable condition is rejected when the breakpoint is added', () => {
  const target = findStmt(PROGRAM, 'x = x + 1');
  const dbg = new Debugger(PROGRAM);
  let threw = false;
  try {
    dbg.addBreakpoint(target.id, 'x >');
  } catch (err) {
    threw = err instanceof ParseError;
  }
  assert(threw, 'addBreakpoint should throw a ParseError for a bad condition');
});

test('conditions: full run without breakpoints matches runs that evaluated erroring conditions', () => {
  const full = runProgram(PROGRAM);
  const dbg = new Debugger(PROGRAM);
  const target = findStmt(PROGRAM, 'x = x + 1');
  dbg.addBreakpoint(target.id, 'x = 1000');
  dbg.runToEnd();
  assertDeepEqual(dbg.output, full.output, 'outputs match');
  assertDeepEqual(
    dbg.trace.map((t) => t.nodeId),
    full.trace.map((t) => t.nodeId),
    'traces match',
  );
});
