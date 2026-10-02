import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MvccStore,
  TransactionAbortedError,
  type Transaction,
} from '../src/mvcc.ts';

/** 构造带初始数据的存储，使后续事务 ID 从 10 开始。 */
function seededStore() {
  const store = new MvccStore({ initialTxnId: 9 });
  const seed = store.begin(); // ID = 9
  seed.write('K1', 'v0');
  seed.commit();
  return store;
}

test('验收场景：T1(10) 未提交写对 T2(11) 不可见，T2 写冲突被 Abort，T1 提交后 T3(12) 读到新值', () => {
  const store = seededStore();

  // T1 (ID=10)：修改 K1，暂不提交
  const t1 = store.begin();
  assert.equal(t1.id, 10);
  t1.write('K1', 'v1');

  // T2 (ID=11)：快照读 K1 只能看到旧版本 v0
  const t2 = store.begin();
  assert.equal(t2.id, 11);
  assert.equal(t2.read('K1'), 'v0');

  // T2 试图写 K1 → 写写冲突 → First-Committer-Wins → T2 被 Abort
  assert.throws(() => t2.write('K1', 'v2'), TransactionAbortedError);
  assert.equal(t2.status, 'aborted');

  // T1 提交成功
  t1.commit();
  assert.equal(t1.status, 'committed');

  // 新启动的 T3 (ID=12) 读到 T1 写入的最新值
  const t3 = store.begin();
  assert.equal(t3.id, 12);
  assert.equal(t3.read('K1'), 'v1');
  t3.commit();
});

test('事务 ID 单调自增', () => {
  const store = new MvccStore();
  const ids = [store.begin().id, store.begin().id, store.begin().id];
  assert.deepEqual(ids, [1, 2, 3]);
});

test('read-your-own-writes：事务能读到自己的未提交写入', () => {
  const store = seededStore();
  const t = store.begin();
  t.write('K1', 'mine');
  assert.equal(t.read('K1'), 'mine');
  t.rollback();
});

test('快照隔离：快照后其他事务的提交对老事务不可见', () => {
  const store = seededStore();
  const t1 = store.begin();
  const t2 = store.begin();
  t2.write('K1', 'v-new');
  t2.commit();
  // t1 的快照在 t2 提交之前，仍读到旧值
  assert.equal(t1.read('K1'), 'v0');
  t1.rollback();
  // 新事务可见
  const t3 = store.begin();
  assert.equal(t3.read('K1'), 'v-new');
});

test('First-Committer-Wins：提交时校验，首提交者获胜', () => {
  const store = seededStore();
  const t1 = store.begin();
  const t2 = store.begin();
  t1.write('K1', 'from-t1');
  t1.commit(); // 首提交者
  // t2 此时写 K1：快照之后已有已提交新版本 → 写时即 Abort
  assert.throws(() => t2.write('K1', 'from-t2'), TransactionAbortedError);
  assert.equal(t2.status, 'aborted');
});

test('写写冲突被 Abort 后，其未提交版本被清除，不污染版本链', () => {
  const store = seededStore();
  const t1 = store.begin();
  const t2 = store.begin();
  t1.write('K1', 'from-t1');
  assert.throws(() => t2.write('K1', 'from-t2'), TransactionAbortedError);
  // 版本链中不存在 t2 的残留版本
  assert.ok(!store.inspect('K1').some((v) => v.txnId === t2.id));
  t1.commit();
  const t3 = store.begin();
  assert.equal(t3.read('K1'), 'from-t1');
});

test('rollback：未提交写入对外不可见且被移除', () => {
  const store = seededStore();
  const t1 = store.begin();
  t1.write('K1', 'dirty');
  t1.rollback();
  assert.equal(store.inspect('K1').every((v) => v.commitTs !== null), true);
  const t2 = store.begin();
  assert.equal(t2.read('K1'), 'v0');
});

test('delete：墓碑对快照读表现为键不存在', () => {
  const store = seededStore();
  const t1 = store.begin();
  t1.delete('K1');
  t1.commit();
  const t2 = store.begin();
  assert.equal(t2.read('K1'), undefined);
});

test('不同键互不冲突，可并行提交', () => {
  const store = seededStore();
  const t1 = store.begin();
  const t2 = store.begin();
  t1.write('A', '1');
  t2.write('B', '2');
  t1.commit();
  t2.commit();
  const t3 = store.begin();
  assert.equal(t3.read('A'), '1');
  assert.equal(t3.read('B'), '2');
});

test('已结束事务不允许再操作', () => {
  const store = seededStore();
  const t: Transaction = store.begin();
  t.commit();
  assert.throws(() => t.read('K1'), /cannot read/);
  assert.throws(() => t.write('K1', 'x'), /cannot write/);
});
