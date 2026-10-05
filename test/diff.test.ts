import { diffTrajectories } from '../src/index.js';
import type { CompareOptions, DiffReport, Segment, TraceEvent } from '../src/types.js';

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log('ok   - ' + name);
  } catch (err) {
    failed++;
    console.error('FAIL - ' + name);
    console.error(err);
  }
}

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error('assertion failed: ' + msg);
}

function assertEq(actual: unknown, expected: unknown, msg: string): void {
  const a = JSON.stringify(actual);
  const b = JSON.stringify(expected);
  if (a !== b) throw new Error(msg + '\n    actual:   ' + a + '\n    expected: ' + b);
}

function ev(action: string, result?: unknown, args?: Record<string, unknown>): TraceEvent {
  const e: TraceEvent = { action: action };
  if (result !== undefined) e.result = result;
  if (args !== undefined) e.args = args;
  return e;
}

function loop(body: TraceEvent[], times: number): Segment {
  return { kind: 'loop', body: body, times: times };
}

function run(events: TraceEvent[]): Segment {
  return { kind: 'run', events: events };
}

function mustDiv(r: DiffReport) {
  if (r.divergence === null) throw new Error('expected a divergence, got equal');
  return r.divergence;
}

const OPTS: CompareOptions = { observable: ['navigate', 'click', 'type', 'read'] };

test('identical traces are equal', () => {
  const a = [ev('navigate', 'ok'), ev('click', 1)];
  const b = [ev('navigate', 'ok'), ev('click', 1)];
  const r = diffTrajectories(a, b, OPTS);
  assert(r.equal, 'should be equal');
  assertEq(r.matchedObservables, 2, 'matched count');
  assertEq(r.divergence, null, 'no divergence');
});

test('different amounts of internal steps are tolerated', () => {
  const left = [ev('navigate', 'a'), ev('log'), ev('think'), ev('click', 1)];
  const right = [ev('navigate', 'a'), ev('log'), ev('click', 1)];
  const r = diffTrajectories(left, right, OPTS);
  assert(r.equal, 'equal despite internal count diff');
  assertEq(r.stats.left.internalEvents, 2, 'left internal');
  assertEq(r.stats.right.internalEvents, 1, 'right internal');
  assertEq(r.matchedObservables, 2, 'matched');
});

test('value mismatch reports path, values and internal window', () => {
  const left = [ev('navigate', 'home'), ev('think'), ev('read', 'hello')];
  const right = [ev('navigate', 'home'), ev('read', 'hallo')];
  const r = diffTrajectories(left, right, OPTS);
  const d = mustDiv(r);
  assertEq(d.kind, 'value-mismatch', 'kind');
  assertEq(d.observableIndex, 1, 'index');
  assertEq(d.valueDiffs.length, 1, 'one diff');
  assertEq(d.valueDiffs[0].path, '$.result', 'path');
  assertEq(d.valueDiffs[0].left, 'hello', 'left value');
  assertEq(d.valueDiffs[0].right, 'hallo', 'right value');
  assertEq(d.window.left.internalTotal, 1, 'left gap total');
  assertEq(d.window.left.internal[0].action, 'think', 'left gap event');
  assertEq(d.window.right.internalTotal, 0, 'right gap total');
  const lastMatched = d.window.left.lastMatchedObservable;
  assert(lastMatched !== null && lastMatched.action === 'navigate', 'last matched context');
  const diverging = d.window.left.divergingObservable;
  assert(diverging !== null && diverging.action === 'read', 'diverging observable');
});

test('args differences are located under $.args', () => {
  const left = [ev('click', 'ok', { x: 1, y: 2 })];
  const right = [ev('click', 'ok', { x: 1, y: 3 })];
  const d = mustDiv(diffTrajectories(left, right, OPTS));
  assertEq(d.valueDiffs[0].path, '$.args.y', 'path');
  assertEq(d.valueDiffs[0].left, 2, 'left');
  assertEq(d.valueDiffs[0].right, 3, 'right');
});

test('ignoreKeys suppresses noisy fields', () => {
  const a = [ev('read', { ts: 1, text: 'x' })];
  const b = [ev('read', { ts: 2, text: 'x' })];
  assert(diffTrajectories(a, b, { observable: ['read'], ignoreKeys: ['ts'] }).equal, 'ignored ts');
  const c = [ev('read', { ts: 1, text: 'y' })];
  const d = mustDiv(diffTrajectories(a, c, { observable: ['read'], ignoreKeys: ['ts'] }));
  assertEq(d.valueDiffs[0].path, '$.result.text', 'path');
});

test('numericTolerance absorbs tiny float differences', () => {
  const a = [ev('read', 0.1)];
  const b = [ev('read', 0.1000001)];
  assert(diffTrajectories(a, b, { observable: ['read'], numericTolerance: 0.001 }).equal, 'within tolerance');
  const d = mustDiv(diffTrajectories(a, b, { observable: ['read'] }));
  assertEq(d.valueDiffs[0].path, '$.result', 'path without tolerance');
});

test('missing action when one trace ends early', () => {
  const left = [ev('navigate'), ev('click')];
  const right = [ev('navigate'), ev('click'), ev('type'), ev('read')];
  const d = mustDiv(diffTrajectories(left, right, OPTS));
  assertEq(d.kind, 'missing-action', 'kind');
  assertEq(d.observableIndex, 2, 'index');
  assertEq(d.left, null, 'left exhausted');
  assert(d.right !== null && d.right.action === 'type', 'right next action');
  assertEq(d.remainingObservables, 2, 'remaining on right');
});

test('swapped adjacent actions are an order mismatch', () => {
  const left = [ev('navigate'), ev('click'), ev('type')];
  const right = [ev('navigate'), ev('type'), ev('click')];
  const d = mustDiv(diffTrajectories(left, right, OPTS));
  assertEq(d.kind, 'order-mismatch', 'kind');
  assertEq(d.observableIndex, 1, 'index');
  assertEq(d.foundInLeftAt, 0, 'right action found in left');
  assertEq(d.foundInRightAt, 0, 'left action found in right');
});

test('inserted action on one side is an order mismatch with one-sided lookahead', () => {
  const left = [ev('navigate'), ev('click')];
  const right = [ev('navigate'), ev('type'), ev('click')];
  const d = mustDiv(diffTrajectories(left, right, OPTS));
  assertEq(d.kind, 'order-mismatch', 'kind');
  assertEq(d.foundInLeftAt, null, 'type not in left');
  assertEq(d.foundInRightAt, 0, 'click found in right');
});

test('loop segment equals its manual expansion', () => {
  const body = [ev('click', 1), ev('log')];
  const expanded: TraceEvent[] = [];
  for (let i = 0; i < 3; i++) {
    expanded.push(ev('click', 1));
    expanded.push(ev('log'));
  }
  const r = diffTrajectories([loop(body, 3)], [run(expanded)], OPTS);
  assert(r.equal, 'loop == expansion');
  assertEq(r.matchedObservables, 3, 'matched');
  assertEq(r.stats.left.totalEvents, 6, 'left total');
  assertEq(r.stats.right.totalEvents, 6, 'right total');
});

test('loop iteration count mismatch surfaces as missing action', () => {
  const body = [ev('click', 1), ev('log')];
  const d = mustDiv(diffTrajectories([loop(body, 2)], [loop(body, 3)], OPTS));
  assertEq(d.kind, 'missing-action', 'kind');
  assertEq(d.observableIndex, 2, 'index');
  assertEq(d.remainingObservables, 1, 'remaining');
  assert(d.right !== null && d.right.action === 'click', 'next action');
});

test('internal-only loops are fast-forwarded without expansion', () => {
  const body = [ev('tick'), ev('tock')];
  const left = [loop(body, 1000000), run([ev('navigate', 'x')])];
  const right = [loop(body, 3), run([ev('navigate', 'x')])];
  const start = Date.now();
  const r = diffTrajectories(left, right, OPTS);
  assert(Date.now() - start < 1000, 'fast-forward must be O(segments), not O(times)');
  assert(r.equal, 'equal');
  assertEq(r.stats.left.internalEvents, 2000000, 'left internal');
  assertEq(r.stats.right.internalEvents, 6, 'right internal');
  assertEq(r.stats.left.totalEvents, 2000001, 'left total');
});

test('internal window keeps only the most recent maxWindow steps', () => {
  const left: TraceEvent[] = [];
  for (let i = 0; i < 10; i++) left.push(ev('internal-' + i));
  left.push(ev('read', 'L'));
  const right = [ev('read', 'R')];
  const d = mustDiv(diffTrajectories(left, right, { observable: ['read'], maxWindow: 3 }));
  assertEq(d.kind, 'value-mismatch', 'kind');
  assertEq(d.window.left.internalTotal, 10, 'total');
  assertEq(d.window.left.internal.length, 3, 'window length');
  assertEq(d.window.left.truncated, true, 'truncated flag');
  assertEq(
    d.window.left.internal.map((e) => e.action),
    ['internal-7', 'internal-8', 'internal-9'],
    'window keeps the tail'
  );
});

test('error status divergence is a value mismatch on status/error', () => {
  const left: TraceEvent[] = [{ action: 'read', result: null, status: 'error', error: 'boom' }];
  const right: TraceEvent[] = [{ action: 'read', result: null, status: 'ok' }];
  const d = mustDiv(diffTrajectories(left, right, OPTS));
  assertEq(d.kind, 'value-mismatch', 'kind');
  const paths = d.valueDiffs.map((v) => v.path);
  assert(paths.indexOf('$.status') !== -1, 'status diff present');
  assert(paths.indexOf('$.error') !== -1, 'error diff present');
});

test('mixed run/loop segments align with a flat trace', () => {
  const left = [
    run([ev('navigate', 'h')]),
    loop([ev('click', 1), ev('log')], 2),
    run([ev('read', 'done')]),
  ];
  const right = [
    run([ev('navigate', 'h'), ev('click', 1), ev('log'), ev('click', 1), ev('log'), ev('read', 'done')]),
  ];
  const r = diffTrajectories(left, right, OPTS);
  assert(r.equal, 'equal');
  assertEq(r.matchedObservables, 4, 'matched');
});

test('only the first divergence is reported', () => {
  const left = [ev('read', 'a'), ev('read', 'b')];
  const right = [ev('read', 'x'), ev('read', 'y')];
  const d = mustDiv(diffTrajectories(left, right, OPTS));
  assertEq(d.observableIndex, 0, 'first divergence index');
  assertEq(d.valueDiffs[0].left, 'a', 'first left value');
});

test('empty traces and empty-vs-nonempty traces', () => {
  assert(diffTrajectories([], [], OPTS).equal, 'both empty');
  const d = mustDiv(diffTrajectories([], [ev('navigate')], OPTS));
  assertEq(d.kind, 'missing-action', 'kind');
  assertEq(d.observableIndex, 0, 'index');
  assertEq(d.remainingObservables, 1, 'remaining');
});

test('object-shaped inputs { events } and { segments } are accepted', () => {
  const left = { events: [ev('navigate', 'x')] };
  const right = { segments: [loop([ev('navigate', 'x')], 1)] };
  assert(diffTrajectories(left, right, OPTS).equal, 'object inputs');
});

test('missing object key is reported with null on the absent side', () => {
  const left = [ev('read', { a: 1 })];
  const right = [ev('read', { a: 1, b: 2 })];
  const d = mustDiv(diffTrajectories(left, right, OPTS));
  assertEq(d.valueDiffs[0].path, '$.result.b', 'path');
  assertEq(d.valueDiffs[0].left, null, 'absent left');
  assertEq(d.valueDiffs[0].right, 2, 'present right');
});

console.log('');
console.log(passed + ' passed, ' + failed + ' failed');
if (failed > 0) process.exit(1);
