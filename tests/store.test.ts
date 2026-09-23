import assert from 'node:assert/strict';
import test from 'node:test';
import { RestoreError } from '../src/errors.ts';
import { Store } from '../src/store.ts';
import { accDef, addDef, makeRegistry, makeStore } from './helpers.ts';

test('事务原子提交：订阅者只看到最终值，且只通知一次', () => {
  const store = makeStore();
  const seen: unknown[] = [];
  store.subscribe('sum', (v) => seen.push(v));
  store.begin().set('a', 10).set('b', 20).commit();
  assert.equal(store.get('sum'), 30);
  assert.deepEqual(seen, [30]);
});

test('读者看不到事务中间态：commit 前读到旧值', () => {
  const store = makeStore();
  const tx = store.begin().set('a', 99);
  assert.equal(store.get('a'), 1);
  assert.equal(store.get('sum'), 3);
  tx.commit();
  assert.equal(store.get('a'), 99);
  assert.equal(store.get('sum'), 101);
});

test('提交失败不污染已发布状态，版本号不变', () => {
  const store = makeStore();
  const versionBefore = store.version;
  assert.throws(
    () => store.begin().set('ghost', 1).commit(),
    (e: unknown) => e instanceof RestoreError && e.code === 'UNKNOWN_NODE',
  );
  assert.equal(store.get('a'), 1);
  assert.equal(store.get('sum'), 3);
  assert.equal(store.version, versionBefore);
});

test('同一事务中后面的非法操作使整批回滚', () => {
  const store = makeStore();
  assert.throws(() =>
    store.begin().set('a', 50).set('sum', 1).commit(),
    (e: unknown) => e instanceof RestoreError && e.code === 'NOT_A_STATE_NODE',
  );
  assert.equal(store.get('a'), 1);
});

test('操作日志可回放：重放得到等价状态', () => {
  const registry = makeRegistry();
  const store = makeStore(registry);
  store.begin().set('a', 7).commit();
  store.begin().set('b', 8).commit();

  const replayed = Store.replay(store.ops(), registry);
  assert.equal(replayed.get('a'), 7);
  assert.equal(replayed.get('b'), 8);
  assert.equal(replayed.get('sum'), 15);
  assert.equal(replayed.get('product'), 56);
  assert.equal(replayed.get('total'), 8); // 0+1 (初始) +7 (set a=7)
  assert.equal(replayed.version, store.version);
});

test('单调环（自反馈累加器）跨提交单调累积', () => {
  const store = makeStore();
  assert.equal(store.get('total'), 1);
  store.begin().set('a', 5).commit();
  assert.equal(store.get('total'), 6);
  store.begin().set('a', 10).commit();
  assert.equal(store.get('total'), 16);
});

test('非单调定义上的自反馈边在定义时即被拒绝', () => {
  const store = new Store(makeRegistry());
  store.begin().defineState('a', 1).commit();
  assert.throws(
    () => store.begin().defineComputed('x', addDef, ['x', 'a']).commit(),
    (e: unknown) => e instanceof RestoreError && e.code === 'MONOTONIC_CYCLE',
  );
});

test('非法求值环在定义时即被拒绝', () => {
  const store = new Store(makeRegistry());
  store.begin().defineState('a', 1).commit();
  assert.throws(
    () =>
      store
        .begin()
        .defineComputed('x', addDef, ['y', 'a'])
        .defineComputed('y', addDef, ['x', 'a'])
        .commit(),
    (e: unknown) => e instanceof RestoreError && e.code === 'ILLEGAL_CYCLE',
  );
  assert.equal(store.has('x'), false);
  assert.equal(store.has('y'), false);
});

test('重复节点 id 被拒绝（共享身份唯一）', () => {
  const store = new Store(makeRegistry());
  assert.throws(
    () => store.begin().defineState('a', 1).defineState('a', 2).commit(),
    (e: unknown) => e instanceof RestoreError && e.code === 'DUPLICATE_NODE',
  );
});

test('取消订阅后不再收到通知', () => {
  const store = makeStore();
  const seen: unknown[] = [];
  const off = store.subscribe('sum', (v) => seen.push(v));
  store.begin().set('a', 2).commit();
  off();
  store.begin().set('a', 3).commit();
  assert.deepEqual(seen, [4]);
});

test('值未变化时不通知', () => {
  const store = makeStore();
  const seen: unknown[] = [];
  store.subscribe('sum', (v) => seen.push(v));
  store.begin().set('a', 1).commit(); // 与旧值相同
  assert.deepEqual(seen, []);
});

test('单调定义缺失自边初值时使用 initial', () => {
  const registry = makeRegistry();
  const store = new Store(registry);
  store.begin().defineState('a', 4).defineComputed('t', accDef, ['t', 'a']).commit();
  assert.equal(store.get('t'), 4);
});
