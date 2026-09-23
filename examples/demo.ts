import { World, CycleError } from '../src/reactive.ts';

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// 1. Diamond: each node recomputes exactly once per update.
{
  show('diamond graph');
  const w = new World();
  const s = w.state('s', 1);
  const b = w.computed('b', () => s.get() + 1);
  const c = w.computed('c', () => s.get() * 2);
  const d = w.computed('d', () => b.get() + c.get());
  console.log('d =', d.get());
  s.set(2);
  console.log('after s=2, d =', d.get());
  console.log('recomputes:', { b: b.recomputeCount, c: c.recomputeCount, d: d.recomputeCount });
}

// 2. Dynamic branch: if(flag, a, b) — after switching, `a` no longer triggers.
{
  show('dynamic branch if(flag, a, b)');
  const w = new World();
  const flag = w.state('flag', true);
  const a = w.state('a', 1);
  const b = w.state('b', 10);
  const sel = w.computed('sel', () => (flag.get() ? a.get() : b.get()));
  console.log('sel =', sel.get(), 'deps =', w.depsOf('sel'));
  flag.set(false);
  console.log('sel =', sel.get(), 'deps =', w.depsOf('sel'));
  const before = sel.recomputeCount;
  a.set(999);
  console.log(`a.set(999): sel = ${sel.get()}, recomputes ${before} -> ${sel.recomputeCount} (dead branch ignored)`);
}

// 3. Batch: subscribers only see the committed snapshot.
{
  show('atomic batch');
  const w = new World();
  const x = w.state('x', 0);
  const y = w.state('y', 0);
  const sum = w.computed('sum', () => x.get() + y.get());
  const seen: number[] = [];
  w.effect('spy', () => seen.push(sum.get()));
  w.batch(() => {
    x.set(1);
    y.set(2);
    x.set(3);
  });
  console.log('effect observed:', seen, '(single notification, final values only)');
}

// 4. Cycle: concrete node chain; breaks and recovers.
{
  show('cycle detection and recovery');
  const w = new World();
  const flag = w.state('flag', true);
  const x = w.state('x', 1);
  const a = w.computed('A', () => (flag.get() ? x.get() : b.get() + 1));
  const b = w.computed('B', () => a.get() * 2);
  console.log('A =', a.get(), 'B =', b.get());
  flag.set(false); // forms A -> B -> A
  try {
    a.get();
  } catch (error) {
    console.log('cycle:', (error as CycleError).message);
  }
  flag.set(true); // breaks the cycle
  x.set(41);
  console.log('recovered: A =', a.get(), 'B =', b.get());
}

// 5. Re-entrant write from a compute callback: next-round isolation.
{
  show('deferred write (next-round isolation)');
  const w = new World();
  const src = w.state('src', 5);
  const mirror = w.state('mirror', 0);
  const c = w.computed('c', () => {
    const v = src.get();
    if (mirror.get() !== v) mirror.set(v); // deferred to the next round
    return v;
  });
  console.log('c =', c.get(), '-> mirror settled at', mirror.get(), 'in the following round');
}

// 6. Replayable operation log.
{
  show('operation log replay');
  const build = () => {
    const w = new World();
    const s = w.state('s', 1);
    const d = w.computed('double', () => s.get() * 2);
    return { w, s, d };
  };
  const live = build();
  live.s.set(2);
  live.w.batch(() => {
    live.s.set(3);
  });
  console.log('log:', JSON.stringify(live.w.log));
  const replica = build();
  replica.w.applyLog(live.w.log);
  console.log('replayed double =', replica.d.get(), '(live:', live.d.get() + ')');
}
