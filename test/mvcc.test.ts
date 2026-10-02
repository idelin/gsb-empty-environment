/** 单元测试：快照隔离、版本链遍历、提交原子性、节点不可变性。 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { MvccStore } from '../src/store.ts';

const KEYS = 8;
const POOL = 1024;

function newStore(initial = 100): MvccStore {
  const store = MvccStore.create(KEYS, POOL);
  store.initAll(initial);
  return store;
}

test('初始快照读到初始值，总和正确', () => {
  const store = newStore(100);
  const snap = store.snapshot();
  for (let k = 0; k < KEYS; k++) assert.equal(store.read(snap, k), 100);
  assert.equal(store.sum(snap), KEYS * 100);
});

test('提交后新快照看到新值', () => {
  const store = newStore();
  store.commit([[3, 42]]);
  const snap = store.snapshot();
  assert.equal(store.read(snap, 3), 42);
  assert.equal(store.read(snap, 0), 100);
});

test('旧快照不受后续提交影响（快照隔离 + 节点不可变）', () => {
  const store = newStore();
  const snap0 = store.snapshot();
  store.commit([[1, 1]]);
  store.commit([[1, 2]]);
  store.commit([[1, 3]]);
  assert.equal(store.read(snap0, 1), 100, '旧快照必须读到历史版本');
  assert.equal(store.read(store.snapshot(), 1), 3, '新快照读到最新版本');
});

test('版本链遍历：不同快照各自定位到正确版本', () => {
  const store = newStore(0);
  const snaps: number[] = [];
  for (let v = 1; v <= 5; v++) {
    store.commit([[2, v * 10]]);
    snaps.push(store.snapshot());
  }
  assert.equal(store.read(snaps[0], 2), 10);
  assert.equal(store.read(snaps[2], 2), 30);
  assert.equal(store.read(snaps[4], 2), 50);
});

test('未提交的版本节点对快照不可见（txId 过滤）', () => {
  const store = newStore();
  const snap = store.snapshot();
  // 模拟“写事务进行中”：节点已挂链但 COMMIT_SEQ 尚未推进。
  store.appendVersion(4, 999, snap + 1);
  assert.equal(store.read(snap, 4), 100, '进行中的写入对当前快照不可见');
  assert.equal(store.read(snap + 1, 4), 999, '提交后对新快照可见');
});

test('多键写事务原子可见：不存在只见一半的快照', () => {
  const store = newStore(1000);
  const before = store.snapshot();
  // 一个事务内更新两个键（转账 250）。
  const txId = store.commit([
    [0, 750],
    [1, 1250],
  ]);
  // 任何快照要么整体看到旧值，要么整体看到新值。
  assert.equal(store.read(before, 0) + store.read(before, 1), 2000);
  assert.equal(store.read(txId, 0) + store.read(txId, 1), 2000);
  assert.equal(store.read(txId, 0), 750);
  assert.equal(store.read(txId, 1), 1250);
});

test('转账序列下每个历史快照总和均守恒', () => {
  const store = newStore(1000);
  const snaps: number[] = [store.snapshot()];
  let a = 1000;
  let b = 1000;
  for (let i = 0; i < 50; i++) {
    const amount = (i % 97) + 1;
    a -= amount;
    b += amount;
    store.commit([
      [0, a],
      [1, b],
    ]);
    snaps.push(store.snapshot());
  }
  for (const snap of snaps) {
    assert.equal(store.sum(snap), KEYS * 1000, `快照 ${snap} 总和必须守恒`);
  }
});

test('节点池耗尽时报错而非损坏数据', () => {
  const store = MvccStore.create(2, 4); // 2 个初始节点 + 2 个余量
  store.initAll(1);
  store.commit([[0, 2]]); // 用掉 1 个
  assert.throws(() => store.commit([[0, 3], [1, 4]]), RangeError);
});
