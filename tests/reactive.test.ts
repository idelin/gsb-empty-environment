import { test } from 'node:test';
import assert from 'node:assert/strict';
import { World, CycleError, UnstableError } from '../src/reactive.ts';

function catchErr(fn: () => unknown): Error {
  try {
    fn();
  } catch (error) {
    return error as Error;
  }
  throw new Error('expected the function to throw');
}

test('dynamic branch: if(flag,a,b) — switching flag unsubscribes the dead branch', () => {
  const w = new World();
  const flag = w.state<boolean>('flag', true);
  const a = w.state('a', 1);
  const b = w.state('b', 10);
  const sel = w.computed('sel', () => (flag.get() ? a.get() : b.get()));

  assert.equal(sel.get(), 1);
  assert.deepEqual(w.depsOf('sel').sort(), ['a', 'flag']);

  a.set(2);
  assert.equal(sel.get(), 2);
  assert.equal(sel.recomputeCount, 2);

  flag.set(false);
  assert.equal(sel.get(), 10);
  assert.deepEqual(w.depsOf('sel').sort(), ['b', 'flag']);
  const count = sel.recomputeCount;

  a.set(99); // dead branch: must not trigger recomputation
  assert.equal(sel.get(), 10);
  assert.equal(sel.recomputeCount, count);

  b.set(11); // live branch still triggers
  assert.equal(sel.get(), 11);
  assert.equal(sel.recomputeCount, count + 1);
});

test('diamond: each node recomputes exactly once per update', () => {
  const w = new World();
  const s = w.state('s', 1);
  const b = w.computed('b', () => s.get() + 1);
  const c = w.computed('c', () => s.get() * 2);
  const d = w.computed('d', () => b.get() + c.get());

  assert.equal(d.get(), 4);
  s.set(2);
  assert.equal(d.get(), 7);
  assert.equal(w.recomputeCount('b'), 2);
  assert.equal(w.recomputeCount('c'), 2);
  assert.equal(w.recomputeCount('d'), 2);
});

test('necessity: unchanged intermediate value stops propagation', () => {
  const w = new World();
  const s = w.state('s', 1);
  const parity = w.computed('parity', () => s.get() % 2);
  const derived = w.computed('derived', () => parity.get() * 10);

  assert.equal(derived.get(), 10);
  s.set(3); // parity unchanged (1) -> derived must not recompute
  assert.equal(derived.get(), 10);
  assert.equal(w.recomputeCount('parity'), 2);
  assert.equal(w.recomputeCount('derived'), 1);
});

test('batch: subscribers observe only the committed snapshot', () => {
  const w = new World();
  const x = w.state('x', 0);
  const y = w.state('y', 0);
  const sum = w.computed('sum', () => x.get() + y.get());
  const seen: number[] = [];
  w.effect('spy', () => {
    seen.push(sum.get());
  });

  assert.deepEqual(seen, [0]);
  w.batch(() => {
    x.set(1);
    y.set(2);
    x.set(3);
  });
  assert.deepEqual(seen, [0, 5]); // one notification, final values only
  assert.equal(w.recomputeCount('sum'), 2);
});

test('deferred write from a compute callback applies in the next round', () => {
  const w = new World();
  const src = w.state('src', 5);
  const mirror = w.state('mirror', 0);
  const seenInside: number[] = [];
  const c = w.computed('c', () => {
    const v = src.get();
    const m = mirror.get();
    seenInside.push(m);
    if (m !== v) mirror.set(v); // re-entrant write
    return v;
  });

  assert.equal(c.get(), 5);
  assert.deepEqual(seenInside, [0]); // the callback ran on the pre-write snapshot
  assert.equal(mirror.get(), 5); // the write landed in the following round

  src.set(7);
  assert.equal(c.get(), 7);
  assert.deepEqual(seenInside, [0, 5]); // the next run observed the settled value
  assert.equal(mirror.get(), 7);
});

test('compute callback errors propagate to readers and recover', () => {
  const w = new World();
  const s = w.state('s', 1);
  const risky = w.computed('risky', () => {
    const v = s.get();
    if (v < 0) throw new Error('negative');
    return v * 10;
  });
  const downstream = w.computed('downstream', () => risky.get() + 1);

  assert.equal(downstream.get(), 11);
  s.set(-1);
  assert.throws(() => downstream.get(), /negative/);
  assert.throws(() => risky.get(), /negative/);
  s.set(2);
  assert.equal(downstream.get(), 21); // recovered
});

test('self-write loop is reported as unstable instead of hanging', () => {
  const w = new World();
  const s = w.state('s', 0);
  const c = w.computed('c', () => {
    const v = s.get();
    s.set(v + 1); // keeps invalidating itself forever
    return v;
  });
  assert.throws(
    () => w.effect('watcher', () => c.get()),
    (error: unknown) => error instanceof UnstableError,
  );
});

test('static cycle returns the concrete node chain', () => {
  const w = new World();
  const refA: { get(): number } = { get: () => 0 };
  const a = w.computed('A', () => refA.get() + 1);
  const b = w.computed('B', () => a.get() + 1);
  refA.get = () => b.get();

  const err = catchErr(() => a.get());
  assert.ok(err instanceof CycleError);
  assert.deepEqual(err.chain, ['A', 'B', 'A']);
  assert.match(err.message, /A -> B -> A/);
});

test('dynamic cycle: forms, reports the chain, then breaks and recovers', () => {
  const w = new World();
  const flag = w.state('flag', true);
  const x = w.state('x', 1);
  const a = w.computed('A', () => (flag.get() ? x.get() : b.get() + 1));
  const b = w.computed('B', () => a.get() * 2);

  assert.equal(a.get(), 1);
  assert.equal(b.get(), 2);

  flag.set(false); // A now reads B while B reads A -> cycle
  const err = catchErr(() => a.get());
  assert.ok(err instanceof CycleError);
  assert.equal(err.chain[0], err.chain[err.chain.length - 1]);
  assert.ok(err.chain.includes('A') && err.chain.includes('B'));
  assert.ok(catchErr(() => b.get()) instanceof CycleError);

  flag.set(true); // break the cycle
  x.set(41);
  assert.equal(a.get(), 41);
  assert.equal(b.get(), 82);
  assert.deepEqual(w.depsOf('A').sort(), ['flag', 'x']);
});

test('operation log replays to identical state', () => {
  const build = () => {
    const w = new World();
    const flag = w.state('flag', true);
    const a = w.state('a', 1);
    const b = w.state('b', 10);
    const sel = w.computed('sel', () => (flag.get() ? a.get() : b.get()));
    const dbl = w.computed('dbl', () => sel.get() * 2);
    return { w, flag, a, b, sel, dbl };
  };

  const live = build();
  live.a.set(2);
  live.w.batch(() => {
    live.flag.set(false);
    live.b.set(20);
  });
  live.a.set(3);
  assert.equal(live.dbl.get(), 40);

  const replayed = build();
  replayed.w.applyLog(live.w.log);
  assert.equal(replayed.dbl.get(), 40);
  assert.equal(replayed.sel.get(), 20);
  assert.deepEqual(
    replayed.w.log.filter((op) => op.type === 'set'),
    live.w.log.filter((op) => op.type === 'set'),
  );
});
