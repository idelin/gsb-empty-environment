import { deepStrictEqual, ok, strictEqual } from 'node:assert';
import { readFileSync } from 'node:fs';
import { replaySlice } from '../src/replay.js';
import { sliceLog, varKey } from '../src/slicer.js';
import type { EventKind, EventLog, Gap, LogEvent, SliceEdge, Value, VarVersion } from '../src/types.js';

interface TestCase {
  name: string;
  fn: () => void;
}

type TestFn = () => void;

const tests: TestCase[] = [];

function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

function vv(name: string, version: number): VarVersion {
  return { name, version };
}

function ev(id: number, kind: EventKind, writes: VarVersion | null, reads: VarVersion[], control: number[], op: string | null, value: Value): LogEvent {
  return { id, kind, writes, reads, control, op, value };
}

function sliceIds(log: EventLog, assertId: number): number[] {
  return sliceLog(log, assertId).events.map((e: LogEvent): number => e.id);
}

test('slice preserves variable versions instead of merging by name', () => {
  const log: EventLog = { events: [
    ev(1, 'const', vv('limit', 0), [], [], null, 100),
    ev(2, 'const', vv('x', 0), [], [], null, 40),
    ev(3, 'const', vv('y', 0), [], [], null, 3),
    ev(4, 'op', vv('total', 0), [vv('x', 0), vv('y', 0)], [], 'mul', 120),
    ev(5, 'const', vv('x', 1), [], [], null, 7),
    ev(6, 'branch', null, [vv('total', 0), vv('limit', 0)], [], 'gt', true),
    ev(7, 'op', vv('total', 1), [vv('total', 0), vv('x', 0)], [6], 'sub', 80),
    ev(8, 'assert', null, [vv('total', 1), vv('limit', 0)], [6], 'gt', false),
  ] };
  deepStrictEqual(sliceIds(log, 8), [1, 2, 3, 4, 6, 7, 8]);
  const slice = sliceLog(log, 8);
  strictEqual(slice.gaps.length, 0);
  ok(!slice.events.some((e: LogEvent): boolean => e.id === 5));
  ok(slice.edges.some((e: SliceEdge): boolean => e.from === 7 && e.to === 2 && e.kind === 'data' && e.variable !== null && varKey(e.variable) === 'x@0'));
  ok(!slice.edges.some((e: SliceEdge): boolean => e.to === 5));
  const report = replaySlice(slice);
  strictEqual(report.verdict, 'match');
  strictEqual(report.actual, false);
});

test('control dependencies are traced transitively', () => {
  const log: EventLog = { events: [
    ev(1, 'const', vv('b', 0), [], [], null, 3),
    ev(2, 'const', vv('a', 0), [], [], null, 5),
    ev(3, 'const', vv('s', 0), [], [], null, 7),
    ev(4, 'branch', null, [vv('a', 0), vv('b', 0)], [], 'gt', true),
    ev(5, 'const', vv('r', 0), [], [4], null, 7),
    ev(6, 'assert', null, [vv('r', 0), vv('s', 0)], [4], 'eq', true),
  ] };
  deepStrictEqual(sliceIds(log, 6), [1, 2, 3, 4, 5, 6]);
  const slice = sliceLog(log, 6);
  ok(slice.edges.some((e: SliceEdge): boolean => e.from === 6 && e.to === 4 && e.kind === 'control'));
  ok(slice.edges.some((e: SliceEdge): boolean => e.from === 5 && e.to === 4 && e.kind === 'control'));
  strictEqual(replaySlice(slice).verdict, 'match');
});

test('missing producer is reported as an explicit gap and blocks replay', () => {
  const log: EventLog = { events: [
    ev(1, 'const', vv('x', 0), [], [], null, 2),
    ev(2, 'const', vv('w', 0), [], [], null, 10),
    ev(3, 'op', vv('y', 0), [vv('x', 0), vv('q', 0)], [], 'add', 12),
    ev(4, 'assert', null, [vv('y', 0), vv('w', 0)], [], 'lt', false),
  ] };
  const slice = sliceLog(log, 4);
  strictEqual(slice.gaps.length, 1);
  const gap = slice.gaps[0];
  strictEqual(gap.kind, 'missing-producer');
  strictEqual(gap.neededBy, 3);
  deepStrictEqual(gap.variable, { name: 'q', version: 0 });
  ok(gap.message.indexOf('q@0') >= 0);
  const report = replaySlice(slice);
  strictEqual(report.verdict, 'indeterminate');
  deepStrictEqual(report.unknownEvents, [3, 4]);
});

test('missing control-dependency event is reported as a gap', () => {
  const log: EventLog = { events: [
    ev(1, 'const', vv('x', 0), [], [], null, 1),
    ev(2, 'const', vv('y', 0), [], [], null, 1),
    ev(3, 'assert', null, [vv('x', 0), vv('y', 0)], [99], 'eq', true),
  ] };
  const slice = sliceLog(log, 3);
  strictEqual(slice.gaps.length, 1);
  strictEqual(slice.gaps[0].kind, 'missing-control');
  strictEqual(slice.gaps[0].controlId, 99);
  strictEqual(slice.gaps[0].neededBy, 3);
  strictEqual(replaySlice(slice).verdict, 'match');
});

test('replay detects a tampered assertion value', () => {
  const log: EventLog = { events: [
    ev(1, 'const', vv('x', 0), [], [], null, 5),
    ev(2, 'assert', null, [vv('x', 0), vv('x', 0)], [], 'eq', false),
  ] };
  const report = replaySlice(sliceLog(log, 2));
  strictEqual(report.verdict, 'mismatch');
  strictEqual(report.actual, true);
  strictEqual(report.expected, false);
});

function loadExample(name: string): EventLog {
  const url = new URL('../examples/' + name, import.meta.url);
  const log: EventLog = JSON.parse(readFileSync(url, 'utf8'));
  return log;
}

test('example log: failing assertion slices and replays to the same value', () => {
  const log = loadExample('failing.json');
  const slice = sliceLog(log, 8);
  strictEqual(slice.gaps.length, 0);
  ok(!slice.events.some((e: LogEvent): boolean => e.id === 5));
  const report = replaySlice(slice);
  strictEqual(report.verdict, 'match');
  strictEqual(report.actual, false);
});

test('example log: truncated log surfaces explicit gaps and indeterminate replay', () => {
  const log = loadExample('gapped.json');
  const slice = sliceLog(log, 8);
  strictEqual(slice.gaps.length, 2);
  ok(slice.gaps.every((g: Gap): boolean => g.kind === 'missing-producer' && g.variable !== null && varKey(g.variable) === 'x@0'));
  const report = replaySlice(slice);
  strictEqual(report.verdict, 'indeterminate');
  deepStrictEqual(report.unknownEvents, [4, 6, 7, 8]);
});

let failures = 0;
for (const t of tests) {
  try {
    t.fn();
    console.log('ok - ' + t.name);
  } catch (err) {
    failures += 1;
    console.error('FAIL - ' + t.name);
    console.error(err);
  }
}
if (failures > 0) {
  console.error(String(failures) + ' of ' + String(tests.length) + ' tests failed');
  process.exit(1);
} else {
  console.log(String(tests.length) + ' tests passed');
}
