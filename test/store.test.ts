import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store, atom, derived } from '../src/index.ts';
import { waitFor } from './helpers.ts';

test('事务原子性：一次 transact 只产生一个快照，读者看不到中间态', () => {
  const store = new Store();
  const seen: Array<{ a: number; b: number }> = [];
  store.subscribe((snapshot) => {
    seen.push({ a: snapshot.a as number, b: snapshot.b as number });
  });

  store.transact([
    { key: 'a', value: 1 },
    { key: 'b', value: 2 },
  ]);

  assert.equal(seen.length, 1, '批量提交只通知一次');
  assert.deepEqual(seen[0], { a: 1, b: 2 }, '读者直接看到事务完成后的状态');
  assert.equal(store.version, 1);
});

test('快照不可变：旧快照不随后续事务改变', () => {
  const store = new Store();
  store.transact([{ key: 'x', value: 1 }]);
  const before = store.snapshot();
  store.transact([{ key: 'x', value: 2 }]);
  assert.equal(before.x, 1);
  assert.equal(store.snapshot().x, 2);
  assert.throws(() => {
    (before as Record<string, unknown>).x = 99;
  }, TypeError);
});

test('状态操作可回放：日志重放得到相同状态，且日志可 JSON 序列化', async () => {
  const store = new Store();
  const query = atom(store, 'query', 'init');
  const upper = derived(store, 'upper', async (get) => get(query).toUpperCase());
  query.set('hello');
  await waitFor(store, 'upper', (s: any) => s.status === 'success');

  // 日志经过 JSON 序列化/反序列化（模拟持久化后恢复）再重放。
  const persisted = JSON.parse(JSON.stringify(store.log));
  const replayed = Store.replay(persisted);

  assert.deepEqual(replayed.snapshot(), store.snapshot());
  assert.deepEqual(replayed.log, store.log);
});

test('跨事务依赖：同一事务里多个源变化，派生只重算一次', async () => {
  const store = new Store();
  const a = atom(store, 'a', 1);
  const b = atom(store, 'b', 2);
  let runs = 0;
  const sum = derived(store, 'sum', async (get) => {
    runs += 1;
    return get(a) + get(b);
  });
  await waitFor(store, 'sum', (s: any) => s.status === 'success');
  assert.equal(runs, 1);

  store.transact([
    { key: 'a', value: 10 },
    { key: 'b', value: 20 },
  ]);
  const final = await waitFor(store, 'sum', (s: any) => s.status === 'success' && s.value === 30);
  assert.equal(final.value, 30);
  assert.equal(runs, 2, '一次事务只触发一次重算');
});
