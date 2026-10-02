/**
 * 运行样例：复现验收边界场景。
 * 运行：node examples/demo.ts   （需要 Node >= 22.6，推荐 >= 23.6）
 */
import {
  MvccStore,
  TransactionAbortedError,
} from '../src/mvcc.ts';

const store = new MvccStore({ initialTxnId: 9 });

// 准备初始数据：K1 = 'v0'（已提交的旧版本）
const seed = store.begin();
seed.write('K1', 'v0');
seed.commit();
console.log(`[seed ] txn ${seed.id} committed: K1 = 'v0'`);

// T1 (ID=10)：修改 K1，暂不提交
const t1 = store.begin();
t1.write('K1', 'v1');
console.log(`[T1   ] txn ${t1.id} wrote K1 = 'v1' (uncommitted)`);

// T2 (ID=11)：快照读 K1，只能看到旧版本
const t2 = store.begin();
console.log(`[T2   ] txn ${t2.id} read K1 -> '${t2.read('K1')}' (snapshot: 只见旧版本)`);

// T2 试图写 K1 → 写写冲突 → First-Committer-Wins → Abort
try {
  t2.write('K1', 'v2');
  console.log('[T2   ] unexpected: write succeeded');
} catch (err) {
  if (err instanceof TransactionAbortedError) {
    console.log(`[T2   ] txn ${t2.id} ABORTED (write-write conflict): ${err.message}`);
  } else {
    throw err;
  }
}

// T1 提交
t1.commit();
console.log(`[T1   ] txn ${t1.id} committed`);

// 新启动的 T3 (ID=12)：读到 T1 写入的最新值
const t3 = store.begin();
console.log(`[T3   ] txn ${t3.id} read K1 -> '${t3.read('K1')}' (读到 T1 提交的最新值)`);
t3.commit();

console.log('\n版本链 K1:', store.inspect('K1'));
