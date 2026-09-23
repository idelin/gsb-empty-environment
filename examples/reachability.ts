/**
 * 样例 1：布尔可达闭包在带环图上单调收敛。
 *
 * 图: s -> a -> b -> c -> a（环），d 不可达。
 * reach(n) = OR(前驱的 reach)，布尔格上单调，收敛到最小不动点。
 */
import { FixpointEngine } from '../src/engine.ts';
import { booleanLattice } from '../src/lattice.ts';

const engine = new FixpointEngine({ scheduler: 'fifo' });

engine.input('s', booleanLattice, true);
engine.derived('a', booleanLattice, ['s', 'c'], ([s, c]) => Boolean(s) || Boolean(c), {
  monotone: true,
});
engine.derived('b', booleanLattice, ['a'], ([a]) => Boolean(a), { monotone: true });
engine.derived('c', booleanLattice, ['b'], ([b]) => Boolean(b), { monotone: true });
engine.derived('d', booleanLattice, [], () => false, { monotone: true });

const result = engine.settle();

console.log('=== 样例 1：布尔可达闭包（带环图）===');
console.log(`状态: ${result.status}，迭代次数: ${result.iterations}`);
for (const [name, value] of result.values) {
  console.log(`  reach(${name}) = ${value}`);
}
console.log(`a 的上升链: [${engine.historyOf('a').join(' -> ')}]（单调不减）`);
