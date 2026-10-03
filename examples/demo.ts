import { MVCCStore, WriteConflictError } from '../src/mvcc.ts';

// 验收场景演示：K1 预置旧值 'v0'，事务 ID 从 10 开始
const store = new MVCCStore({ firstTxnId: 10, seed: { K1: 'v0' } });

console.log('初始状态: K1 = "v0" (已提交)\n');

const t1 = store.begin();
console.log(`T1 (ID=${t1.id}) 开始`);
store.write(t1, 'K1', 'v1');
console.log(`T1 (ID=${t1.id}) 写入 K1 = "v1"（未提交）\n`);

const t2 = store.begin();
console.log(`T2 (ID=${t2.id}) 开始`);
console.log(`T2 (ID=${t2.id}) 读 K1 => "${store.read(t2, 'K1')}"（快照隔离，只见旧版本）`);

try {
  store.write(t2, 'K1', 'v2');
} catch (err) {
  if (err instanceof WriteConflictError) {
    console.log(`T2 (ID=${t2.id}) 写 K1 => 写写冲突，事务被 Abort`);
    console.log(`  原因: ${err.message}`);
  } else {
    throw err;
  }
}
console.log(`T2 (ID=${t2.id}) 状态: ${store.stateOf(t2)}\n`);

store.commit(t1);
console.log(`T1 (ID=${t1.id}) 提交成功（首写获胜）\n`);

const t3 = store.begin();
console.log(`T3 (ID=${t3.id}) 开始`);
console.log(`T3 (ID=${t3.id}) 读 K1 => "${store.read(t3, 'K1')}"（读到 T1 提交的最新值）`);
store.commit(t3);
