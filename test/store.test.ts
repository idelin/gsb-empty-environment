import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/index.ts';
import type { Notification } from '../src/index.ts';

test('基本订阅：收到通知，版本递增', () => {
  const store = new Store(0);
  const seen: Notification<number>[] = [];
  store.subscribe((n) => seen.push(n));

  store.set(1);
  store.set(2);

  assert.equal(seen.length, 2);
  assert.equal(seen[0].version, 1);
  assert.equal(seen[0].state, 1);
  assert.equal(seen[0].sinceVersion, 0);
  assert.equal(seen[1].version, 2);
  assert.equal(seen[1].sinceVersion, 1);
  assert.equal(store.getVersion(), 2);
});

test('回调中重入修改：观察器看到严格递增版本', () => {
  const store = new Store(0);
  const versions: number[] = [];
  store.subscribe((n) => {
    versions.push(n.version);
    if (n.version < 5) {
      store.set(n.version + 1);
    }
  });

  store.set(1);

  assert.deepEqual(versions, [1, 2, 3, 4, 5]);
  for (let i = 1; i < versions.length; i++) {
    assert.ok(versions[i] > versions[i - 1], '版本必须严格递增');
  }
});

test('深度递归修改不会栈溢出（迭代式派发）', () => {
  const store = new Store(0);
  const DEPTH = 100_000;
  let delivered = 0;
  store.subscribe((n) => {
    delivered++;
    if (n.version < DEPTH) {
      store.set(n.version + 1);
    }
  });

  store.set(1);

  assert.equal(delivered, DEPTH);
  assert.equal(store.getState(), DEPTH);
  assert.equal(store.getVersion(), DEPTH);
});

test('batch 合并：一批修改只触发一次通知，changes 按序包含全部变更', () => {
  const store = new Store(0);
  const seen: Notification<number>[] = [];
  store.subscribe((n) => seen.push(n));

  store.batch(() => {
    store.set(1, 'a');
    store.set(2, 'b');
    store.set(3, 'c');
  });

  assert.equal(seen.length, 1);
  assert.equal(seen[0].version, 3);
  assert.equal(seen[0].state, 3);
  assert.equal(seen[0].sinceVersion, 0);
  assert.deepEqual(seen[0].changes.map((c) => c.version), [1, 2, 3]);
  assert.deepEqual(seen[0].changes.map((c) => c.action), ['a', 'b', 'c']);
});

test('隐式合并：回调中重入修改时，慢观察器按批次合并通知', () => {
  const store = new Store(0);
  const aSeen: number[] = [];
  const bSeen: Notification<number>[] = [];
  // A 先注册，回调中连续修改两次；B 后注册，应在后续批次看到合并结果。
  store.subscribe((n) => {
    aSeen.push(n.version);
    if (n.version === 1) {
      store.set(2);
      store.set(3);
    }
  });
  store.subscribe((n) => bSeen.push(n));

  store.set(1);

  // B 第一批收到 v1；A 的重入修改 v2、v3 合并为 B 的第二批。
  assert.equal(bSeen.length, 2);
  assert.equal(bSeen[0].version, 1);
  assert.equal(bSeen[1].version, 3);
  assert.equal(bSeen[1].sinceVersion, 1);
  assert.deepEqual(bSeen[1].changes.map((c) => c.version), [2, 3]);
  // A 所见版本同样严格递增。
  assert.deepEqual(aSeen, [1, 3]);
});

test('取消订阅：之后不再收到通知', () => {
  const store = new Store(0);
  const seen: number[] = [];
  const unsub = store.subscribe((n) => seen.push(n.version));

  store.set(1);
  unsub();
  store.set(2);
  store.set(3);

  assert.deepEqual(seen, [1]);
  assert.equal(store.getObserverCount(), 0);
});

test('取消订阅在派发中途立即生效', () => {
  const store = new Store(0);
  const bSeen: number[] = [];
  let unsubB: () => void = () => {};
  // A 先注册，回调里取消 B；B 后注册，本应收到 v1，但因已被取消而跳过。
  store.subscribe(() => {
    unsubB();
  });
  unsubB = store.subscribe((n) => bSeen.push(n.version));

  store.set(1);

  assert.deepEqual(bSeen, []);
});

test('异常隔离：一个观察器抛错不影响其他观察器，错误聚合抛出', () => {
  const store = new Store(0);
  const seen: number[] = [];
  store.subscribe(() => {
    throw new Error('boom-1');
  });
  store.subscribe((n) => seen.push(n.version));
  store.subscribe(() => {
    throw new Error('boom-2');
  });

  let err: unknown;
  try {
    store.set(1);
  } catch (e) {
    err = e;
  }
  assert.ok(err instanceof AggregateError);
  assert.equal(err.errors.length, 2);
  assert.deepEqual(seen, [1], '其余观察器仍被通知');
});

test('异常隔离：onError 回调接收错误，不再聚合抛出', () => {
  const errors: unknown[] = [];
  const store = new Store(0, { onError: (e) => errors.push(e) });
  const seen: number[] = [];
  store.subscribe(() => {
    throw new Error('boom');
  });
  store.subscribe((n) => seen.push(n.version));

  store.set(1);

  assert.equal(errors.length, 1);
  assert.deepEqual(seen, [1]);
});

test('通知期间新增的观察器：收不到在途批次，从订阅时刻版本开始', () => {
  const store = new Store(0);
  const lateSeen: Notification<number>[] = [];
  store.subscribe((n) => {
    if (n.version === 1) {
      // 在 v1 派发期间订阅；随后 v1 批次不再涉及它。
      store.subscribe((m) => lateSeen.push(m));
      store.set(2);
    }
  });

  store.set(1);

  // 新观察器订阅于 v1 提交之后，只见 v2，且 sinceVersion 为 1。
  assert.equal(lateSeen.length, 1);
  assert.equal(lateSeen[0].version, 2);
  assert.equal(lateSeen[0].sinceVersion, 1);
  assert.deepEqual(lateSeen[0].changes.map((c) => c.version), [2]);
});

test('通知期间新增的观察器：同批次后续版本也不会补发历史', () => {
  const store = new Store(0);
  const lateSeen: number[] = [];
  store.subscribe(() => {
    store.subscribe((m) => lateSeen.push(m.version));
  });

  store.batch(() => {
    store.set(1);
    store.set(2);
    store.set(3);
  });

  // 新观察器在批次派发时才订阅，整个批次（v1..v3）都不投递给它。
  assert.deepEqual(lateSeen, []);

  store.set(4);
  assert.deepEqual(lateSeen, [4]);
});

test('update 与 getState/getVersion', () => {
  const store = new Store({ count: 0 });
  const seen: number[] = [];
  store.subscribe((n) => seen.push(n.state.count));

  store.update((s) => ({ count: s.count + 1 }));
  store.update((s) => ({ count: s.count + 1 }));

  assert.deepEqual(seen, [1, 2]);
  assert.deepEqual(store.getState(), { count: 2 });
  assert.equal(store.getVersion(), 2);
});

test('嵌套 batch：最外层结束时统一派发', () => {
  const store = new Store(0);
  const seen: Notification<number>[] = [];
  store.subscribe((n) => seen.push(n));

  store.batch(() => {
    store.set(1);
    store.batch(() => {
      store.set(2);
    });
    store.set(3);
  });

  assert.equal(seen.length, 1);
  assert.deepEqual(seen[0].changes.map((c) => c.version), [1, 2, 3]);
});

test('重入修改与在途批次的状态一致性：批次内 state 与 version 匹配', () => {
  const store = new Store(0);
  const bSeen: Notification<number>[] = [];
  store.subscribe((n) => {
    if (n.version === 1) {
      store.set(2); // A 在 B 收到 v1 之前推进了全局状态
    }
  });
  store.subscribe((n) => bSeen.push(n));

  store.set(1);

  // B 收到的 v1 通知中，state 必须是 v1 的快照，而非已被推进的 v2。
  assert.equal(bSeen[0].version, 1);
  assert.equal(bSeen[0].state, 1);
  assert.equal(bSeen[1].version, 2);
  assert.equal(bSeen[1].state, 2);
});
