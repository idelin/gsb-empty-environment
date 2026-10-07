import { test } from 'node:test';
import assert from 'node:assert/strict';
import { run } from '../src/interpreter.ts';
import { Debugger, type PauseInfo } from '../src/debugger.ts';

function lineOf(source: string, needle: string): number {
  const lines = source.split('\n');
  const index = lines.findIndex((line) => line.includes(needle));
  assert.notEqual(index, -1, `line containing ${JSON.stringify(needle)} not found`);
  return index + 1;
}

function continueToEnd(dbg: Debugger): PauseInfo[] {
  const pauses: PauseInfo[] = [];
  while (true) {
    const info = dbg.continue();
    if (info.state === 'done') break;
    pauses.push(info);
  }
  return pauses;
}

test('loop: full run matches stepped run, trace pauses before every node', () => {
  const src = [
    'let total: number = 0;',
    'for (let i: number = 1; i <= 5; i = i + 1) {',
    '  total = total + i;',
    '}',
    'print(total);',
  ].join('\n');

  const full = run(src);
  assert.deepEqual(full.output, ['15']);

  const dbg = new Debugger(src);
  // Breakpoints trigger before node execution: at the very first pause the
  // first statement has not run yet, so `total` is still undeclared.
  const first = dbg.step();
  assert.equal(first.state, 'paused');
  assert.equal(first.line, 1);
  assert.equal(dbg.getVariable('total'), undefined);

  while (!dbg.done) dbg.step();
  assert.deepEqual(dbg.output, full.output);
  assert.ok(dbg.trace.length > 10, 'step trace visits many nodes');
  assert.ok(dbg.trace.every((entry) => entry.reason === 'step'));
  const kinds = new Set(dbg.trace.map((entry) => entry.kind));
  assert.ok(kinds.has('VarDecl') && kinds.has('For') && kinds.has('Call'));
});

test('loop: breakpoint fires before node execution; continue never re-hits the same instance', () => {
  const src = ['let i: number = 0;', 'while (i < 3) {', '  i = i + 1;', '}', 'print(i);'].join('\n');
  const dbg = new Debugger(src);
  dbg.setBreakpointAtLine(lineOf(src, 'i = i + 1'));

  const seen: Array<{ i: unknown; seq: number }> = [];
  while (true) {
    const info = dbg.continue();
    if (info.state === 'done') break;
    seen.push({ i: dbg.getVariable('i'), seq: dbg.trace[dbg.trace.length - 1].seq });
  }

  // One hit per iteration, each a distinct execution instance (unique seq),
  // and the breakpoint fires before the assignment runs (i not yet updated).
  assert.deepEqual(seen.map((s) => s.i), [0, 1, 2]);
  const seqs = seen.map((s) => s.seq);
  assert.deepEqual([...seqs].sort((a, b) => a - b), seqs);
  assert.equal(new Set(seqs).size, seqs.length);
  assert.deepEqual(dbg.output, ['3']);
});

test('recursion: factorial breakpoint observes each frame, step trace matches full run', () => {
  const src = [
    'function fact(n: number): number {',
    '  if (n <= 1) {',
    '    return 1;',
    '  }',
    '  return n * fact(n - 1);',
    '}',
    'print(fact(5));',
  ].join('\n');

  assert.deepEqual(run(src).output, ['120']);

  const dbg = new Debugger(src);
  dbg.setBreakpointAtLine(lineOf(src, 'return n * fact(n - 1);'));
  const frames: unknown[] = [];
  while (true) {
    const info = dbg.continue();
    if (info.state === 'done') break;
    frames.push(dbg.getVariable('n'));
  }
  // Outermost frame pauses first, then each nested call.
  assert.deepEqual(frames, [5, 4, 3, 2]);
  assert.deepEqual(dbg.output, ['120']);

  const stepped = new Debugger(src);
  while (!stepped.done) stepped.step();
  assert.deepEqual(stepped.output, run(src).output);
});

test('pause: modifying a variable while paused changes subsequent execution', () => {
  const src = [
    'let total: number = 0;',
    'for (let i: number = 1; i <= 5; i = i + 1) {',
    '  total = total + i;',
    '}',
    'print(total);',
  ].join('\n');

  assert.deepEqual(run(src).output, ['15']);

  const dbg = new Debugger(src);
  dbg.setBreakpointAtLine(lineOf(src, 'total = total + i;'), 'i === 3');

  const hit = dbg.continue();
  assert.equal(hit.state, 'paused');
  assert.equal(hit.reason, 'breakpoint');
  assert.equal(dbg.getVariable('i'), 3);
  assert.equal(dbg.getVariable('total'), 3); // 0 + 1 + 2, current line not yet executed

  dbg.setVariable('total', 100);
  const end = dbg.continue();
  assert.equal(end.state, 'done');
  assert.deepEqual(dbg.output, ['112']); // 100 + 3 + 4 + 5
  assert.deepEqual(dbg.conditionErrors, []);
});

test('conditional breakpoint: evaluation is side-effect free', () => {
  const src = [
    'let calls: number = 0;',
    'function bump(): number {',
    '  calls = calls + 1;',
    '  return calls;',
    '}',
    'let x: number = 0;',
    'for (let i: number = 0; i < 3; i = i + 1) {',
    '  x = x + i;',
    '}',
    'print(x);',
    'print(calls);',
  ].join('\n');

  // Condition calls a function that mutates a global and never holds.
  const dbg = new Debugger(src);
  dbg.setBreakpointAtLine(lineOf(src, 'x = x + i;'), 'bump() > 100');
  const end = dbg.continue();
  assert.equal(end.state, 'done');
  assert.deepEqual(dbg.output, ['3', '0']); // `calls` untouched by condition evals
  assert.deepEqual(dbg.conditionErrors, []);

  // Condition containing an assignment pauses (truthy) but must not leak.
  const dbg2 = new Debugger(src);
  dbg2.setBreakpointAtLine(lineOf(src, 'x = x + i;'), 'x = 999');
  const xs: unknown[] = [];
  while (true) {
    const info = dbg2.continue();
    if (info.state === 'done') break;
    xs.push(dbg2.getVariable('x'));
  }
  assert.deepEqual(xs, [0, 0, 1]); // real `x` never becomes 999
  assert.deepEqual(dbg2.output, ['3', '0']);
});

test('conditional breakpoint: errors are collected separately and do not stop execution', () => {
  const src = ['let x: number = 0;', 'for (let i: number = 0; i < 3; i = i + 1) {', '  x = x + i;', '}', 'print(x);'].join('\n');

  // Runtime error inside the condition (undefined variable).
  const dbg = new Debugger(src);
  const nodeId = dbg.setBreakpointAtLine(lineOf(src, 'x = x + i;'), 'missing > 1');
  const end = dbg.continue();
  assert.equal(end.state, 'done');
  assert.deepEqual(dbg.output, ['3']);
  assert.equal(dbg.conditionErrors.length, 3);
  for (const entry of dbg.conditionErrors) {
    assert.equal(entry.nodeId, nodeId);
    assert.equal(entry.condition, 'missing > 1');
    assert.match(entry.message, /missing/);
  }

  // Parse error inside the condition.
  const dbg2 = new Debugger(src);
  dbg2.setBreakpointAtLine(lineOf(src, 'x = x + i;'), 'i ===');
  const end2 = dbg2.continue();
  assert.equal(end2.state, 'done');
  assert.deepEqual(dbg2.output, ['3']);
  assert.equal(dbg2.conditionErrors.length, 3);
  assert.match(dbg2.conditionErrors[0].message, /parse error/);
});

test('step ignores breakpoints and walks node by node', () => {
  const src = ['let a: number = 1;', 'a = a + 1;', 'print(a);'].join('\n');
  const dbg = new Debugger(src);
  dbg.setBreakpointAtLine(lineOf(src, 'a = a + 1;'));
  while (!dbg.done) {
    const info = dbg.step();
    if (info.state === 'paused') assert.equal(info.reason, 'step');
  }
  assert.deepEqual(dbg.output, ['2']);
  assert.ok(dbg.trace.every((entry) => entry.reason === 'step'));
});

test('clearBreakpoint removes the breakpoint', () => {
  const src = ['let i: number = 0;', 'while (i < 2) {', '  i = i + 1;', '}', 'print(i);'].join('\n');
  const dbg = new Debugger(src);
  const nodeId = dbg.setBreakpointAtLine(lineOf(src, 'i = i + 1;'));
  const hit = dbg.continue();
  assert.equal(hit.state, 'paused');
  dbg.clearBreakpoint(nodeId);
  const end = dbg.continue();
  assert.equal(end.state, 'done');
  assert.deepEqual(dbg.output, ['2']);
});
