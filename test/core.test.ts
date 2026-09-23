import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CycleError,
  FnRegistry,
  ReactiveSystem,
  replaySystem,
} from '../src/core.ts';

function makeSystem(): ReactiveSystem {
  const registry = new FnRegistry();
  return new ReactiveSystem(registry);
}

test('basic computed node pulls lazily and caches', () => {
  const sys = makeSystem();
  const s = sys.state<number>('s', 1);
  const c = sys.computed<number>('c', 'c', (cx) => (cx.get('s') as number) + 1);
  assert.equal(c.get(), 2);
  assert.equal(c.get(), 2);
  assert.equal(sys.stats()['c'], 1, 'computed runs once despite two reads');
  s.set(5);
  assert.equal(c.get(), 6);
  assert.equal(sys.stats()['c'], 2);
});

test('diamond dependency: join node recomputes exactly once', () => {
  const sys = makeSystem();
  const s = sys.state<number>('s', 0);
  sys.computed<number>('left', 'left', (cx) => (cx.get('s') as number) + 1);
  sys.computed<number>('right', 'right', (cx) => (cx.get('s') as number) + 2);
  sys.computed<number>('join', 'join', (cx) => {
    return (cx.get('left') as number) + (cx.get('right') as number);
  });
  sys.effect('view', 'view', (cx) => cx.get('join'));

  const before = sys.stats();
  s.set(10);
  const after = sys.stats();
  assert.equal(after['left']! - before['left']!, 1);
  assert.equal(after['right']! - before['right']!, 1);
  assert.equal(after['join']! - before['join']!, 1, 'join must not recompute twice');
  assert.equal(after['view']! - before['view']!, 1, 'effect notifies exactly once');
  assert.deepEqual(sys.effectInfo('view').lastOutput, 23);
});

test('dynamic branch: if(flag,a,b) drops and acquires dependencies', () => {
  const sys = makeSystem();
  const flag = sys.state<boolean>('flag', true);
  const a = sys.state<number>('a', 1);
  const b = sys.state<number>('b', 10);
  const c = sys.computed<number>('c', 'c', (cx) => {
    return cx.get('flag') ? (cx.get('a') as number) : (cx.get('b') as number);
  });
  sys.effect('view', 'view', (cx) => cx.get('c'));

  assert.equal(c.get(), 1);
  a.set(2);
  assert.equal(c.get(), 2);
  const runsWithA = sys.stats()['c']!;

  // Switch the branch: b becomes a dependency, a must stop triggering c.
  flag.set(false);
  assert.equal(c.get(), 10);
  const runsAfterSwitch = sys.stats()['c']!;
  assert.ok(runsAfterSwitch > runsWithA);

  a.set(99);
  assert.equal(sys.stats()['c'], runsAfterSwitch, 'a must not trigger c after branch switched away');
  assert.equal(sys.effectInfo('view').runs, 3, 'effect saw: initial, a=2, flag=false');

  b.set(50);
  assert.equal(c.get(), 50);
  assert.ok(sys.stats()['c']! > runsAfterSwitch, 'b now triggers c');

  // Switch back: a re-acquired.
  flag.set(true);
  assert.equal(c.get(), 99);
});

test('cycle detection returns the concrete node chain', () => {
  const sys = makeSystem();
  sys.state<number>('s0', 0);
  sys.computed<number>('x', 'x', (cx) => (cx.get('y') as number) + 1);
  sys.computed<number>('y', 'y', (cx) => (cx.get('x') as number) + 1);

  let caught: unknown;
  try {
    sys.effect('view', 'view', (cx) => cx.get('x'));
  } catch (err) {
    caught = err;
  }
  const errors = sys.commitErrors.filter((e) => e.effect === 'view');
  assert.equal(errors.length, 1);
  const cycle = errors[0]!.error;
  assert.ok(cycle instanceof CycleError);
  assert.deepEqual((cycle as CycleError).chain, ['x', 'y', 'x']);
  assert.equal(caught, undefined, 'cycle is delivered through the failing effect, not thrown out of set');
});

test('cycle forms dynamically via flags, breaks, and nodes recover', () => {
  const sys = makeSystem();
  const fa = sys.state<number>('fa', 0);
  const fb = sys.state<number>('fb', 0);
  const s0 = sys.state<number>('s0', 7);
  const s1 = sys.state<number>('s1', 9);
  sys.computed<number>('ca', 'ca', (cx) => {
    return cx.get('fa') === 1 ? (cx.get('cb') as number) : (cx.get('s0') as number);
  });
  sys.computed<number>('cb', 'cb', (cx) => {
    return cx.get('fb') === 1 ? (cx.get('ca') as number) : (cx.get('s1') as number);
  });
  sys.effect('view', 'view', (cx) => [cx.get('ca'), cx.get('cb')]);

  assert.deepEqual(sys.effectInfo('view').lastOutput, [7, 9]);

  // Form the cycle by enabling both links.
  sys.tx(() => {
    fa.set(1);
    fb.set(1);
  });
  const failed = sys.commitErrors.find((e) => e.effect === 'view');
  assert.ok(failed);
  assert.ok(failed!.error instanceof CycleError);
  assert.deepEqual((failed!.error as CycleError).chain, ['ca', 'cb', 'ca']);

  // Break one link: both nodes must recover using the live graph.
  fa.set(0);
  assert.equal(sys.commitErrors.length, 0);
  assert.deepEqual(sys.effectInfo('view').lastOutput, [7, 7]);

  // Re-form then break the other link: errors must be sticky until invalidated.
  fa.set(1);
  assert.ok(sys.commitErrors.find((e) => e.effect === 'view')?.error instanceof CycleError);
  fb.set(0);
  assert.deepEqual(sys.effectInfo('view').lastOutput, [9, 9]);
});

test('transaction: one consistent snapshot, single notification, no intermediate states', () => {
  const sys = makeSystem();
  const a = sys.state<number>('a', 0);
  const b = sys.state<number>('b', 0);
  const sum = sys.computed<number>('sum', 'sum', (cx) => {
    return (cx.get('a') as number) + (cx.get('b') as number);
  });
  sys.effect('view', 'view', (cx) => {
    return { a: cx.get('a'), b: cx.get('b'), sum: cx.get('sum') };
  });
  const notificationsBefore = sys.effectInfo('view').notifications.length;

  sys.tx(() => {
    a.set(1);
    a.set(2); // repeated change to the same state inside one transaction
    b.set(3);
  });

  const info = sys.effectInfo('view');
  assert.equal(info.notifications.length - notificationsBefore, 1, 'exactly one notification for the whole transaction');
  assert.deepEqual(info.lastOutput, { a: 2, b: 3, sum: 5 });

  // Readers inside an explicit transaction see staged state writes but
  // computed nodes expose the previous committed snapshot.
  let observedState: unknown;
  let observedComputed: unknown;
  sys.tx(() => {
    a.set(40);
    observedState = a.get();
    observedComputed = sum.get();
  });
  assert.equal(observedState, 40);
  assert.equal(observedComputed, 5, 'computed reads inside a tx see the committed snapshot');
  assert.deepEqual(sys.effectInfo('view').lastOutput, { a: 40, b: 3, sum: 43 });
});

test('throwing computed: error stored, retried after invalidation, deps stay valid', () => {
  const sys = makeSystem();
  const s = sys.state<number>('s', 5);
  const c = sys.computed<number>('c', 'c', (cx) => {
    const v = cx.get('s') as number;
    if (v < 0) throw new Error(`negative: ${v}`);
    return v * 2;
  });
  sys.effect('view', 'view', (cx) => cx.get('c'));

  assert.equal(c.get(), 10);
  s.set(-1);
  assert.equal(sys.commitErrors.length, 1);
  const snap = sys.snapshot()['c']!;
  assert.equal(snap.status, 'error');
  assert.match(snap.status === 'error' ? snap.message : '', /negative/);

  // Fixing the dependency invalidates the error and the node recomputes.
  s.set(3);
  assert.equal(sys.commitErrors.length, 0);
  assert.equal(c.get(), 6);
  assert.deepEqual(sys.effectInfo('view').lastOutput, 6);
});

test('re-entrant write from a computed is isolated to the next round', () => {
  const sys = makeSystem();
  const s = sys.state<number>('s', 0);
  const t = sys.state<number>('t', 0);
  sys.computed<number>('c', 'c', (cx) => {
    const v = cx.get('s') as number;
    const u = cx.get('t') as number;
    if (u < v) cx.set('t', v); // converge t upward; deferred to the next round
    return v;
  });
  sys.effect('watch', 'watch', (cx) => {
    return { s: cx.get('s'), t: cx.get('t'), c: cx.get('c') };
  });
  const before = sys.effectInfo('watch').notifications.length;

  s.set(1);

  const notes = sys.effectInfo('watch').notifications.slice(before) as {
    s: number; t: number; c: number;
  }[];
  // Round 1 sees the committed s=1 with old t=0; round 2 sees the deferred t=1.
  assert.equal(notes.length, 2);
  assert.deepEqual(notes[0], { s: 1, t: 0, c: 1 });
  assert.deepEqual(notes[1], { s: 1, t: 1, c: 1 });

  // No further rounds once converged.
  s.set(1);
  assert.equal(sys.effectInfo('watch').notifications.length, before + 2);
  assert.equal(t.get(), 1);
});

test('operation log replays into an identical system', () => {
  const registry = new FnRegistry();
  const sys = new ReactiveSystem(registry);
  const s = sys.state<number>('s', 0);
  sys.computed<number>('double', 'double', (cx) => (cx.get('s') as number) * 2);
  sys.effect('out', 'out', (cx) => cx.get('double'));
  sys.tx(() => {
    s.set(3);
    s.set(4);
  });
  s.set(8);

  // Function bodies are not serializable, so replay shares the fn registry.
  const replay = replaySystem(registry, sys.log);
  assert.deepEqual(replay.snapshot(), sys.snapshot());
  assert.deepEqual(replay.effectInfo('out').notifications, sys.effectInfo('out').notifications);
});
