// Runnable tour of the reactive core: diamond, dynamic branch, transaction,
// cycle detection, and re-entrant writes. Run with: npm run demo

import { CycleError, FnRegistry, ReactiveSystem } from '../src/core.ts';

function showStats(sys: ReactiveSystem, label: string): void {
  console.log(`  recompute counts ${label}:`, sys.stats());
}

// --- 1. Diamond: the join recomputes exactly once per change ---------------
{
  console.log('\n[1] diamond: s -> left,right -> join');
  const sys = new ReactiveSystem(new FnRegistry());
  const s = sys.state<number>('s', 1);
  sys.computed<number>('left', 'left', (cx) => (cx.get('s') as number) + 1);
  sys.computed<number>('right', 'right', (cx) => (cx.get('s') as number) + 2);
  sys.computed<number>('join', 'join', (cx) =>
    (cx.get('left') as number) + (cx.get('right') as number),
  );
  sys.effect('view', 'view', (cx) => cx.get('join'));
  console.log('  join =', sys.effectInfo('view').lastOutput);
  showStats(sys, 'after initial run');
  s.set(10);
  console.log('  after s=10, join =', sys.effectInfo('view').lastOutput);
  showStats(sys, 'after one write (each node +1, join not recomputed twice)');
}

// --- 2. Dynamic branch: if(flag,a,b) ---------------------------------------
{
  console.log('\n[2] dynamic branch: c = flag ? a : b');
  const sys = new ReactiveSystem(new FnRegistry());
  const flag = sys.state<boolean>('flag', true);
  const a = sys.state<number>('a', 1);
  const b = sys.state<number>('b', 100);
  const c = sys.computed<number>('c', 'c', (cx) =>
    cx.get('flag') ? (cx.get('a') as number) : (cx.get('b') as number),
  );
  console.log('  c =', c.get());
  flag.set(false);
  console.log('  flag=false -> c =', c.get());
  const runs = sys.stats()['c']!;
  a.set(999);
  console.log(`  a=999 (dead branch): c recomputed ${sys.stats()['c']! - runs} times, c =`, c.get());
  b.set(200);
  console.log('  b=200 (live branch): c =', c.get());
}

// --- 3. Transaction: one snapshot, one notification ------------------------
{
  console.log('\n[3] transaction: repeated writes, single consistent notification');
  const sys = new ReactiveSystem(new FnRegistry());
  const x = sys.state<number>('x', 0);
  const y = sys.state<number>('y', 0);
  sys.effect('view', 'view', (cx) => ({ x: cx.get('x'), y: cx.get('y') }));
  sys.tx(() => {
    x.set(1);
    x.set(2); // overwritten before anyone can observe it
    y.set(3);
  });
  const info = sys.effectInfo('view');
  console.log('  notifications after tx:', info.notifications, '(x=1 intermediate never observed)');
}

// --- 4. Cycle: concrete chain, dynamic form/break --------------------------
{
  console.log('\n[4] cycle detection with concrete node chain');
  const sys = new ReactiveSystem(new FnRegistry());
  const fa = sys.state<number>('fa', 0);
  const fb = sys.state<number>('fb', 0);
  sys.state<number>('s0', 7);
  sys.state<number>('s1', 9);
  sys.computed<number>('ca', 'ca', (cx) =>
    cx.get('fa') === 1 ? (cx.get('cb') as number) : (cx.get('s0') as number),
  );
  sys.computed<number>('cb', 'cb', (cx) =>
    cx.get('fb') === 1 ? (cx.get('ca') as number) : (cx.get('s1') as number),
  );
  sys.effect('view', 'view', (cx) => [cx.get('ca'), cx.get('cb')]);
  console.log('  flags off:', sys.effectInfo('view').lastOutput);
  sys.tx(() => {
    fa.set(1);
    fb.set(1);
  });
  const err = sys.effectInfo('view').error;
  console.log('  flags on :', err instanceof CycleError ? err.message : err);
  fa.set(0);
  console.log('  break fa :', sys.effectInfo('view').lastOutput, '(recovered)');
}

// --- 5. Re-entrant write: isolated to the next round -----------------------
{
  console.log('\n[5] re-entrant write from a computed (next-round isolation)');
  const sys = new ReactiveSystem(new FnRegistry());
  const s = sys.state<number>('s', 0);
  sys.state<number>('t', 0);
  sys.computed<number>('c', 'c', (cx) => {
    const v = cx.get('s') as number;
    if ((cx.get('t') as number) < v) cx.set('t', v);
    return v;
  });
  sys.effect('watch', 'watch', (cx) => ({
    s: cx.get('s'),
    t: cx.get('t'),
    c: cx.get('c'),
  }));
  s.set(5);
  console.log('  notifications:', sys.effectInfo('watch').notifications);
  console.log('  (round 1 sees committed t=0; the deferred write lands in round 2)');
}
