import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OptimisticStore } from '../src/store.ts';

interface Doc {
  title: string;
  likes: number;
  nested: { a: number; b: number };
}

const baseDoc = (): Doc => ({ title: 'draft', likes: 0, nested: { a: 1, b: 2 } });

test('乐观补丁立即对读者可见', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['title'], value: 'hello' }]);
  assert.equal(store.state.title, 'hello');
});

test('A 乐观写后 B 再写，A 失败只撤销自身效果，不覆盖 B', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-A', 1, [{ kind: 'set', path: ['title'], value: 'A' }]);
  store.applyOptimistic('op-B', 1, [{ kind: 'set', path: ['title'], value: 'B' }]);

  const result = store.reject('op-A', 1);

  assert.deepEqual(result, { applied: true, opId: 'op-A', status: 'rejected' });
  assert.equal(store.state.title, 'B', 'B 的后续写入必须保留');
});

test('失败撤销恢复被自身覆盖、且无人再写的字段', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-A', 1, [
    { kind: 'set', path: ['nested', 'a'], value: 100 },
    { kind: 'set', path: ['nested', 'b'], value: 200 },
  ]);
  store.applyOptimistic('op-B', 1, [{ kind: 'set', path: ['nested', 'b'], value: 300 }]);

  store.reject('op-A', 1);

  assert.equal(store.state.nested.a, 1, '只有 A 写过的字段回退到基值');
  assert.equal(store.state.nested.b, 300, 'B 覆盖过的字段保留 B 的值');
});

test('确认响应按 opId + version 匹配，版本不符则忽略', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['likes'], value: 5 }]);

  const wrong = store.confirm('op-1', 2);
  assert.equal(wrong.applied, false);
  if (!wrong.applied) assert.equal(wrong.reason, 'version-mismatch');
  assert.equal(store.log[0].status, 'pending', '操作仍是 pending');

  const right = store.confirm('op-1', 1);
  assert.deepEqual(right, { applied: true, opId: 'op-1', status: 'confirmed' });
  assert.equal(store.log[0].status, 'confirmed');
});

test('重复确认幂等：状态与快照引用均不变', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['likes'], value: 5 }]);
  store.confirm('op-1', 1);
  const snapshot = store.state;

  const again = store.confirm('op-1', 1);

  assert.equal(again.applied, false);
  if (!again.applied) assert.equal(again.reason, 'already-resolved');
  assert.equal(store.state, snapshot, '重复确认不重新发布快照');
  assert.equal(store.state.likes, 5);
});

test('迟到的旧响应不回退已确认的新状态', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['likes'], value: 5 }]);
  store.confirm('op-1', 1);

  const late = store.reject('op-1', 1);

  assert.equal(late.applied, false);
  assert.equal(store.state.likes, 5, '迟到的 reject 不得撤销已确认的效果');
});

test('操作重试升级版本后，旧版本的迟到响应被忽略', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['title'], value: 'v1' }]);
  store.reject('op-1', 1);
  store.applyOptimistic('op-1', 2, [{ kind: 'set', path: ['title'], value: 'v2' }]);

  const stale = store.confirm('op-1', 1);

  assert.equal(stale.applied, false);
  if (!stale.applied) assert.equal(stale.reason, 'version-mismatch');
  assert.equal(store.state.title, 'v2');
});

test('未知操作的响应被安全忽略', () => {
  const store = new OptimisticStore(baseDoc());
  const result = store.confirm('ghost', 1);
  assert.equal(result.applied, false);
  if (!result.applied) assert.equal(result.reason, 'unknown-op');
  assert.deepEqual(store.state, baseDoc());
});

test('读者看不到事务中间态：多步操作只发布一次', () => {
  const store = new OptimisticStore(baseDoc());
  const seen: string[] = [];
  store.subscribe((snapshot) => seen.push(JSON.stringify(snapshot)));

  store.transact((tx) => {
    tx.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['title'], value: 'T' }]);
    tx.applyOptimistic('op-2', 1, [{ kind: 'set', path: ['likes'], value: 9 }]);
  });

  assert.equal(seen.length, 1, '事务只触发一次发布');
  assert.deepEqual(JSON.parse(seen[0]), { ...baseDoc(), title: 'T', likes: 9 });
});

test('事务抛错时什么都不发布', () => {
  const store = new OptimisticStore(baseDoc());
  const seen: string[] = [];
  store.subscribe((snapshot) => seen.push(JSON.stringify(snapshot)));

  assert.throws(() =>
    store.transact((tx) => {
      tx.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['title'], value: 'T' }]);
      throw new Error('boom');
    }),
  );

  assert.equal(seen.length, 0);
  assert.deepEqual(store.state, baseDoc());
});

test('发布给读者的快照是深度不可变的', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['title'], value: 'x' }]);
  const snapshot = store.state;

  assert.throws(() => {
    (snapshot as { title: string }).title = 'hacked';
  }, TypeError);
  assert.throws(() => {
    (snapshot.nested as { a: number }).a = 999;
  }, TypeError);
  assert.equal(store.state.title, 'x');
  assert.equal(store.state.nested.a, 1);
});

test('状态可从操作日志完整回放，且支持重放到任意历史点', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['title'], value: 'one' }]);
  store.applyOptimistic('op-2', 1, [{ kind: 'set', path: ['likes'], value: 7 }]);
  store.reject('op-1', 1);
  store.confirm('op-2', 1);

  const replayed = OptimisticStore.replay(baseDoc(), store.log.map((r) => ({ ...r })));
  assert.deepEqual(replayed, store.state, '重放结果与当前状态一致');

  const atFirst = store.replayTo(1);
  assert.equal(atFirst.title, 'one', '重放到 seq=1 时 op-1 尚未被拒');
  assert.equal(atFirst.likes, 0);
});

test('compact 折叠已确认操作后不改变对外状态', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['likes'], value: 3 }]);
  store.confirm('op-1', 1);
  store.applyOptimistic('op-2', 1, [{ kind: 'set', path: ['title'], value: 'pending' }]);
  const before = JSON.stringify(store.state);

  store.compact();

  assert.equal(JSON.stringify(store.state), before);
  assert.deepEqual(store.log.map((r) => r.opId), ['op-2'], '日志只保留未确认操作');
});

test('同一 opId 在 pending 期间禁止重复使用', () => {
  const store = new OptimisticStore(baseDoc());
  store.applyOptimistic('op-1', 1, [{ kind: 'set', path: ['likes'], value: 1 }]);
  assert.throws(() => store.applyOptimistic('op-1', 2, []), /still pending/);
});
