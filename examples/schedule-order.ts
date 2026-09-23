/**
 * 样例 3：同一带环图在 fifo / lifo / priority 三种调度下，
 * 收敛到同一个最小不动点（迭代次数可以不同）。
 */
import { FixpointEngine, type SchedulerKind } from '../src/engine.ts';
import { booleanLattice } from '../src/lattice.ts';

function buildGraph(scheduler: SchedulerKind): FixpointEngine {
  const engine = new FixpointEngine({ scheduler });
  engine.input('s', booleanLattice, true);
  engine.derived('a', booleanLattice, ['s', 'c'], ([s, c]) => Boolean(s) || Boolean(c), {
    monotone: true,
  });
  engine.derived('b', booleanLattice, ['a', 'e'], ([a, e]) => Boolean(a) || Boolean(e), {
    monotone: true,
  });
  engine.derived('c', booleanLattice, ['b'], ([b]) => Boolean(b), { monotone: true });
  engine.derived('e', booleanLattice, ['c'], ([c]) => Boolean(c), { monotone: true });
  engine.derived('d', booleanLattice, [], () => false, { monotone: true });
  return engine;
}

console.log('=== 样例 3：调度顺序不影响最小不动点 ===');
const snapshots = new Map<string, string>();
for (const kind of ['fifo', 'lifo', 'priority'] as const) {
  const result = buildGraph(kind).settle();
  const serialized = JSON.stringify([...result.values].sort());
  snapshots.set(kind, serialized);
  console.log(`  ${kind.padEnd(8)} 状态=${result.status} 迭代=${result.iterations} 值=${serialized}`);
}
const distinct = new Set(snapshots.values());
console.log(distinct.size === 1 ? '  三种调度结果一致 ✔' : '  结果不一致 ✘');
