import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MVCCStore,
  WriteConflictError,
  TransactionNotActiveError,
} from '../src/mvcc.ts';

test('验收场景：快照隔离读 + 写写冲突首写获胜', () => {
  // 预置已提交旧值 K1 = 'v0'（引导版本，不占用事务 ID），首个事务 ID = 10
  const store = new MVCCStore({ firstTxnId: 10, seed: { K1: 'v0' } });

  // T1 (ID=10) 修改 K1，暂不提交
  const t1 = store.begin();
  assert.equal(t1.id, 10);
  store.write(t1, 'K1', 'v1');

  // T2 (ID=11) 读 K1：只能看到旧版本 'v0'（快照隔离）
  const t2 = store.begin();
  assert.equal(t2.id, 11);
  assert.equal(store.read(t2, 'K1'), 'v0');

  // T2 试图写 K1：与 T1 的未提交写冲突，被 Abort
  assert.throws(() => store.write(t2, 'K1', 'v2'), WriteConflictError);
  assert.equal(store.stateOf(t2), 'aborted');

  // T1 提交成功（首写获胜）
  store.commit(t1);
  assert.equal(store.stateOf(t1), 'committed');

  // 新启动的 T3 (ID=12) 读到 T1 写入的最新值
  const t3 = store.begin();
  assert.equal(t3.id, 12);
  assert.equal(store.read(t3, 'K1'), 'v1');
  store.commit(t3);
});

test('单调自增事务 ID', () => {
  const store = new MVCCStore();
  const ids = [store.begin().id, store.begin().id, store.begin().id];
  assert.deepEqual(ids, [1, 2, 3]);
});

test('快照隔离：读不到事务启动后提交的版本', () => {
  const store = new MVCCStore({ seed: { K: 'old' } });
  const reader = store.begin();

  const writer = store.begin();
  store.write(writer, 'K', 'new');
  store.commit(writer);

  // reader 的快照在 writer 提交之前，仍读到旧值
  assert.equal(store.read(reader, 'K'), 'old');

  // 新事务可见新值
  const fresh = store.begin();
  assert.equal(store.read(fresh, 'K'), 'new');
});

test('读自己的未提交写（read-your-own-writes）', () => {
  const store = new MVCCStore({ seed: { K: 'old' } });
  const tx = store.begin();
  store.write(tx, 'K', 'mine');
  assert.equal(store.read(tx, 'K'), 'mine');
  store.abort(tx);
  // 中止后缓冲写被丢弃
  const other = store.begin();
  assert.equal(store.read(other, 'K'), 'old');
});

test('首写获胜：提交时发现快照后被他人提交则中止', () => {
  const store = new MVCCStore({ seed: { K: 'v0' } });
  const t1 = store.begin();
  const t2 = store.begin();

  // t1 先写并先提交
  store.write(t1, 'K', 'from-t1');
  store.commit(t1);

  // t2 的快照早于 t1 的提交，写同一 key 触发冲突被中止
  assert.throws(() => store.write(t2, 'K', 'from-t2'), WriteConflictError);
  assert.equal(store.stateOf(t2), 'aborted');

  // 最终值为首写提交者 t1 的值
  const t3 = store.begin();
  assert.equal(store.read(t3, 'K'), 'from-t1');
});

test('写不同 key 的并发事务互不冲突', () => {
  const store = new MVCCStore();
  const t1 = store.begin();
  const t2 = store.begin();
  store.write(t1, 'A', '1');
  store.write(t2, 'B', '2');
  store.commit(t1);
  store.commit(t2);
  const t3 = store.begin();
  assert.equal(store.read(t3, 'A'), '1');
  assert.equal(store.read(t3, 'B'), '2');
});

test('已结束事务不可再读写', () => {
  const store = new MVCCStore({ seed: { K: 'v' } });
  const tx = store.begin();
  store.commit(tx);
  assert.throws(() => store.read(tx, 'K'), TransactionNotActiveError);
  assert.throws(() => store.write(tx, 'K', 'x'), TransactionNotActiveError);
});

test('读取不存在的 key 返回 undefined', () => {
  const store = new MVCCStore();
  const tx = store.begin();
  assert.equal(store.read(tx, 'missing'), undefined);
});
