import { test } from 'node:test';
import assert from 'node:assert/strict';
import { VectorClock } from '../src/vectorClock.ts';
import { VersionedStore, type StoreEvent } from '../src/store.ts';

type Store = VersionedStore<string>;
type Event = StoreEvent<string>;

function makeReplicas(): [Store, Store, Store] {
  return [
    new VersionedStore<string>('A'),
    new VersionedStore<string>('B'),
    new VersionedStore<string>('C'),
  ];
}

/** 收集若干副本的本地事件日志。 */
function gather(...stores: Store[]): Event[] {
  return stores.flatMap((s) => s.log);
}

/** 把事件逐个投递给目标副本。 */
function deliver(events: Event[], target: Store): void {
  for (const e of events) target.applyEvent(e);
}

/** 双向交换（gossip）一轮：from 的日志全部投递给 to，反之亦然。 */
function exchange(a: Store, b: Store): void {
  deliver(a.log, b);
  deliver(b.log, a);
}

/** 确定性 PRNG（mulberry32），保证乱序测试可复现。 */
function mulberry32(seed: number): () => number {
  let s = seed;
  return () => {
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function shuffled<T>(items: T[], seed: number): T[] {
  const rand = mulberry32(seed);
  const out = [...items];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

function values(store: Store, key: string): string[] {
  return store
    .get(key)
    .versions.map((v) => v.value)
    .sort();
}

test('向量时钟：支配、并发、相等与归并', () => {
  const a1 = new VectorClock({ A: 1 });
  const a2 = new VectorClock({ A: 2 });
  const b1 = new VectorClock({ B: 1 });

  assert.equal(a2.compare(a1), 'after');
  assert.equal(a1.compare(a2), 'before');
  assert.ok(a2.dominates(a1));
  assert.ok(!a1.dominates(a2));
  assert.equal(a1.compare(b1), 'concurrent');
  assert.ok(a1.equals(new VectorClock({ A: 1 })));

  const merged = a1.merged(b1);
  assert.ok(merged.dominates(a1));
  assert.ok(merged.dominates(b1));
  assert.equal(merged.compare(new VectorClock({ A: 1, B: 1 })), 'equal');

  // 零分量视为不存在
  assert.ok(new VectorClock({ A: 1, B: 0 }).equals(a1));
});

test('离线并发修改同一键：并发版本全部保留', () => {
  const [a, b, c] = makeReplicas();
  a.put('k', 'from-A');
  b.put('k', 'from-B');
  c.put('k', 'from-C');

  // 两两交换后，三个并发版本都必须在每个副本上保留
  exchange(a, b);
  exchange(b, c);
  exchange(a, c);

  for (const replica of [a, b, c]) {
    assert.deepEqual(values(replica, 'k'), ['from-A', 'from-B', 'from-C']);
  }
});

test('显式解决：覆盖调用者见过的全部版本，传播后全网收敛为单版本', () => {
  const [a, b, c] = makeReplicas();
  a.put('k', 'from-A');
  b.put('k', 'from-B');
  exchange(a, b);
  deliver(gather(a, b), c);

  // A 读到两个并发版本，显式解决
  const { versions, context } = a.get('k');
  assert.equal(versions.length, 2);
  a.resolve('k', 'resolved', context);
  assert.deepEqual(values(a, 'k'), ['resolved']);

  // 解决事件传播后，旧版本在所有副本上被回收
  deliver(a.log, b);
  deliver(gather(a, b), c);
  for (const replica of [a, b, c]) {
    assert.deepEqual(values(replica, 'k'), ['resolved']);
    assert.equal(replica.fingerprint(), a.fingerprint());
  }
});

test('解决不得误删调用者未见的并发版本', () => {
  const [a, b] = [new VersionedStore<string>('A'), new VersionedStore<string>('B')];
  a.put('k', 'v1');
  deliver(a.log, b); // B 见到 v1

  // A 基于 v1 的上下文准备解决；与此同时 B 离线写入 v2（A 从未见过）
  const { context } = a.get('k');
  b.put('k', 'v2');
  a.resolve('k', 'resolved-by-A', context);

  // 双向传播：v2 与解决版本并发，两边都必须保留 v2
  exchange(a, b);
  for (const replica of [a, b]) {
    assert.deepEqual(values(replica, 'k'), ['resolved-by-A', 'v2']);
  }
  assert.equal(a.fingerprint(), b.fingerprint());
});

test('交换传播：多轮两两 gossip 后三副本集合一致', () => {
  const [a, b, c] = makeReplicas();
  a.put('x', 'a1');
  b.put('x', 'b1');
  c.put('y', 'c1');
  a.put('y', 'a1');

  // 交换两轮，足以全网收敛
  for (let round = 0; round < 2; round++) {
    exchange(a, b);
    exchange(b, c);
    exchange(a, c);
  }

  assert.equal(a.fingerprint(), b.fingerprint());
  assert.equal(b.fingerprint(), c.fingerprint());
  assert.deepEqual(values(a, 'x'), ['a1', 'b1']);
  assert.deepEqual(values(a, 'y'), ['a1', 'c1']); // 两个并发写入都保留
});

test('重复传播：同一事件流重复投递是幂等的', () => {
  const [a, b, c] = makeReplicas();
  a.put('k', 'a1');
  b.put('k', 'b1');
  const { context } = a.get('k');
  a.resolve('k', 'r', context);

  const events = gather(a, b);
  deliver(events, c);
  const once = c.fingerprint();
  deliver(events, c); // 重复投递整批
  deliver(events, c);
  assert.equal(c.fingerprint(), once);

  // 逐事件重复
  const d = new VersionedStore<string>('D');
  for (const e of events) {
    d.applyEvent(e);
    d.applyEvent(e);
  }
  assert.equal(d.fingerprint(), once);
});

test('乱序传播：不同副本按不同顺序接收，最终集合一致', () => {
  const [a, b, c] = makeReplicas();
  // 离线阶段：三个副本各自产生事件
  a.put('k1', 'a1');
  a.put('k2', 'a1');
  b.put('k1', 'b1');
  c.put('k2', 'c1');
  c.put('k3', 'c1');
  b.put('k3', 'b1');

  const events = gather(a, b, c);

  // 三个全新的“下游”副本，各自按不同的乱序（含重复）接收同一事件集
  const receivers = [
    new VersionedStore<string>('R1'),
    new VersionedStore<string>('R2'),
    new VersionedStore<string>('R3'),
  ];
  receivers.forEach((r, i) => {
    const order = shuffled(events, 1000 + i);
    deliver(order, r);
    deliver(shuffled(order, 2000 + i).slice(0, 3), r); // 混入重复投递
  });

  // 原三副本互相全量交换后也收敛
  deliver(events, a);
  deliver(events, b);
  deliver(events, c);

  const expected = a.fingerprint();
  assert.equal(b.fingerprint(), expected);
  assert.equal(c.fingerprint(), expected);
  for (const r of receivers) assert.equal(r.fingerprint(), expected);

  assert.deepEqual(values(a, 'k1'), ['a1', 'b1']);
  assert.deepEqual(values(a, 'k2'), ['a1', 'c1']);
  assert.deepEqual(values(a, 'k3'), ['b1', 'c1']);
});

test('乱序 + 解决：解决事件先于被解决版本到达，最终仍收敛', () => {
  const [a, b] = [new VersionedStore<string>('A'), new VersionedStore<string>('B')];
  a.put('k', 'v1');
  b.put('k', 'v2');
  exchange(a, b);
  const { context } = a.get('k');
  a.resolve('k', 'final', context);

  const events = gather(a, b); // 含 v1、v2、resolve
  // 逆序投递：resolve 最先到达
  const reversed = [...events].reverse();
  const c = new VersionedStore<string>('C');
  deliver(reversed, c);
  assert.deepEqual(values(c, 'k'), ['final']);

  // 正序、乱序、重复投递结果一致
  for (const seed of [7, 42, 99]) {
    const r = new VersionedStore<string>('R' + seed);
    deliver(shuffled(events, seed), r);
    deliver(shuffled(events, seed * 3), r);
    assert.equal(r.fingerprint(), c.fingerprint());
  }
  assert.equal(a.fingerprint(), c.fingerprint());
});
