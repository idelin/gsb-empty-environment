import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MessageBus } from '../src/bus.ts';
import { Replica } from '../src/replica.ts';
import { Store } from '../src/store.ts';
import type { Schema } from '../src/ops.ts';

const schema: Schema = {
  title: { kind: 'lww' },
  status: { kind: 'lww' },
  tags: { kind: 'mv', resolver: 'preserve' },
  priority: { kind: 'mv', resolver: 'max' },
};

function makeCluster(ids: string[]) {
  const bus = new MessageBus();
  const replicas = new Map(ids.map((id) => [id, new Replica(id, schema, bus)]));
  return { bus, replicas };
}

test('不同字段的并发修改可以合并', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;
  const b = replicas.get('B')!;

  bus.partition('A', 'B');
  a.set('title', 'from-A');
  b.set('status', 'done');
  bus.deliver(); // 分区期间消息滞留
  assert.deepEqual(a.snapshot(), { title: 'from-A' });
  assert.deepEqual(b.snapshot(), { status: 'done' });

  bus.heal('A', 'B');
  bus.deliver();
  assert.deepEqual(a.snapshot(), { title: 'from-A', status: 'done' });
  assert.deepEqual(b.snapshot(), { title: 'from-A', status: 'done' });
});

test('同字段冲突：LWW 按 (lamport, replicaId) 显式决胜', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;
  const b = replicas.get('B')!;

  bus.partition('A', 'B');
  a.set('title', 'A-wins-by-id'); // lamport 相同(1)，replicaId 大者胜
  b.set('title', 'B-loses');
  bus.heal('A', 'B');
  bus.deliver();

  // 两副本收敛到同一确定结果：lamport 相同 -> replicaId 'B' > 'A'
  assert.equal(a.snapshot().title, 'B-loses');
  assert.equal(b.snapshot().title, 'B-loses');
});

test('同字段冲突：LWW 较新的 lamport 覆盖较旧的', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;
  const b = replicas.get('B')!;

  a.set('title', 'v1');
  bus.deliver(); // B 先收到 v1
  b.set('title', 'v2-newer'); // B 基于 v1 再写，lamport 更大
  bus.deliver();

  assert.equal(a.snapshot().title, 'v2-newer');
  assert.equal(b.snapshot().title, 'v2-newer');
});

test('同字段冲突：多值寄存器 preserve 保留全部并发值', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;
  const b = replicas.get('B')!;

  bus.partition('A', 'B');
  a.set('tags', 'red');
  b.set('tags', 'blue');
  bus.heal('A', 'B');
  bus.deliver();

  assert.deepEqual([...a.store.conflicts('tags')].sort(), ['blue', 'red']);
  assert.deepEqual([...b.store.conflicts('tags')].sort(), ['blue', 'red']);
  // 因果覆盖：A 看到冲突后写入新值，并发旧值被取代
  a.set('tags', 'resolved');
  bus.deliver();
  assert.equal(b.snapshot().tags, 'resolved');
  assert.deepEqual(b.store.conflicts('tags'), []);
});

test('同字段冲突：多值寄存器 max 按明确规则决胜', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;
  const b = replicas.get('B')!;

  bus.partition('A', 'B');
  a.set('priority', 3);
  b.set('priority', 7);
  bus.heal('A', 'B');
  bus.deliver();

  assert.equal(a.snapshot().priority, 7);
  assert.equal(b.snapshot().priority, 7);
});

test('重复消息不双应用', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;
  const b = replicas.get('B')!;

  let sideEffectCount = 0;
  b.onSideEffect(() => sideEffectCount++);

  const batch = a.transaction((tx) => tx.set('title', 'hello'));
  bus.injectDuplicate(batch, 'B'); // 模拟 at-least-once 重投
  bus.injectDuplicate(batch, 'B');
  bus.deliver();

  assert.equal(b.snapshot().title, 'hello');
  assert.equal(b.store.appliedCount, 1); // 只应用一次
  assert.equal(sideEffectCount, 1); // 副作用只触发一次
});

test('断线重放不双应用', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;
  const b = replicas.get('B')!;

  bus.partition('A', 'B');
  a.set('title', 't1');
  a.set('status', 'open');
  bus.deliver(); // 滞留
  bus.heal('A', 'B');
  bus.deliver(); // B 收到两条
  assert.equal(b.store.appliedCount, 2);

  // 重连后全量重放 A 的保留日志：全部命中去重
  b.syncFrom(['A']);
  assert.equal(b.store.appliedCount, 2);
  assert.deepEqual(b.snapshot(), { title: 't1', status: 'open' });
});

test('读者看不到事务中间态', () => {
  const { replicas } = makeCluster(['A']);
  const a = replicas.get('A')!;

  const seenDuring: unknown[] = [];
  a.transaction((tx) => {
    tx.set('title', 'atomic-title');
    seenDuring.push(a.snapshot().title); // 事务未提交，读者不可见
    tx.set('status', 'open');
    seenDuring.push(a.snapshot().status);
    seenDuring.push(a.snapshot().title);
  });

  assert.deepEqual(seenDuring, [undefined, undefined, undefined]);
  assert.deepEqual(a.snapshot(), { title: 'atomic-title', status: 'open' }); // 提交后原子可见
});

test('状态操作可回放：从日志重建等价状态', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;
  const b = replicas.get('B')!;

  bus.partition('A', 'B');
  a.set('title', 'doc');
  b.set('tags', 'x');
  a.set('priority', 9);
  bus.heal('A', 'B');
  bus.deliver();

  const rebuilt = Store.replay(schema, a.store.opLog());
  assert.deepEqual(rebuilt.snapshot(), a.snapshot());

  const rebuiltB = Store.replay(schema, b.store.opLog());
  assert.deepEqual(rebuiltB.snapshot(), b.snapshot());
  assert.deepEqual(rebuilt.snapshot(), rebuiltB.snapshot()); // 重放后同样收敛
});

test('本地回声不重复触发副作用', () => {
  const { bus, replicas } = makeCluster(['A', 'B']);
  const a = replicas.get('A')!;

  const events: string[] = [];
  a.onSideEffect((e) => events.push(`${e.op.opId}@${e.origin}`));

  a.set('title', 'echo-test');
  bus.deliver(); // 总线把消息回投给 A 自身（本地回声）

  assert.deepEqual(events, ['A:1@local']); // 只触发一次，回声被去重
  assert.equal(a.store.appliedCount, 1);
});
