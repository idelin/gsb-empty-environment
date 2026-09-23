/**
 * 样例 4：事务原子提交 + 操作日志回放。
 */
import { FixpointEngine } from '../src/engine.ts';
import { booleanLattice } from '../src/lattice.ts';
import { Store } from '../src/store.ts';

function buildEngine(): FixpointEngine {
  const engine = new FixpointEngine();
  engine.input('s1', booleanLattice);
  engine.input('s2', booleanLattice);
  engine.derived('a', booleanLattice, ['s1'], ([s1]) => Boolean(s1), { monotone: true });
  engine.derived('b', booleanLattice, ['a', 's2'], ([a, s2]) => Boolean(a) || Boolean(s2), {
    monotone: true,
  });
  engine.derived('c', booleanLattice, ['b'], ([b]) => Boolean(b), { monotone: true });
  return engine;
}

const store = new Store(buildEngine(), buildEngine);

console.log('=== 样例 4：事务与回放 ===');
console.log(`初始: version=${store.version} a=${store.read('a')} c=${store.read('c')}`);

// 读者在事务提交前看不到缓冲中的写
const tx = store.begin();
tx.set('s1', true);
console.log(`事务中（未提交）: a=${store.read('a')}（读者仍看到旧快照）`);
tx.commit();
console.log(`提交 1 后: version=${store.version} a=${store.read('a')} c=${store.read('c')}`);

store.begin().set('s2', true).commit();
console.log(`提交 2 后: version=${store.version} b=${store.read('b')} c=${store.read('c')}`);

console.log(`操作日志: ${JSON.stringify(store.log)}`);

const replayed = store.replay();
const live = JSON.stringify([...store.snapshot()].sort());
const redo = JSON.stringify([...replayed].sort());
console.log(`回放状态与当前状态一致: ${live === redo ? '是 ✔' : '否 ✘'}`);
