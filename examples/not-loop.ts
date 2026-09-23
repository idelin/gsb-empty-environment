/**
 * 样例 2：x = NOT x 被运行时守卫拒绝，而不是无限振荡；
 * 迭代预算耗尽时标记 non-converged。
 */
import { FixpointEngine, NonMonotoneError } from '../src/engine.ts';
import { booleanLattice } from '../src/lattice.ts';

console.log('=== 样例 2a：x = NOT x（谎报单调）===');
const liar = new FixpointEngine();
liar.derived('x', booleanLattice, ['x'], ([x]) => !x, { monotone: true });
try {
  liar.settle();
  console.log('  意外：未被拒绝');
} catch (error) {
  if (error instanceof NonMonotoneError) {
    console.log(`  已拒绝（NonMonotoneError）：${error.message}`);
    console.log('  系统在有限步内终止，没有无限振荡');
  } else {
    throw error;
  }
}

console.log('=== 样例 2b：迭代预算耗尽 ===');
const CHAIN = 50;
const bounded = new FixpointEngine({ budget: 10 });
bounded.input('n0', booleanLattice, true);
for (let i = 1; i <= CHAIN; i++) {
  bounded.derived(`n${i}`, booleanLattice, [`n${i - 1}`], ([prev]) => Boolean(prev), {
    monotone: true,
  });
}
const result = bounded.settle();
console.log(`  链长 ${CHAIN}，预算 10 -> 状态: ${result.status}，已用迭代: ${result.iterations}`);
