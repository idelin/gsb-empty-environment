import assert from "node:assert/strict";
import { test } from "node:test";
import { Store } from "../src/store.ts";

test("基础: set 触发通知,版本递增", () => {
  const store = new Store(0);
  const seen: Array<[number, number]> = [];
  store.subscribe((state, version) => seen.push([state, version]));

  store.set(1);
  store.set(2);

  assert.deepEqual(seen, [[1, 1], [2, 2]]);
  assert.equal(store.get(), 2);
  assert.equal(store.getVersion(), 2);
});

test("重入: 回调中再次修改状态,版本严格递增", () => {
  const store = new Store(0);
  const seen: Array<[number, number]> = [];
  store.subscribe((state, version) => {
    seen.push([state, version]);
    if (state < 3) store.set(state + 1);
  });

  store.set(1);

  assert.deepEqual(seen, [[1, 1], [2, 2], [3, 3]]);
  assert.equal(store.get(), 3);
  const versions = seen.map(([, v]) => v);
  for (let i = 1; i < versions.length; i++) {
    assert.ok(versions[i] > versions[i - 1], "版本必须严格递增");
  }
});

test("重入: 深层递归修改不会栈溢出", () => {
  const depth = 50_000;
  const store = new Store(0);
  let calls = 0;
  store.subscribe((state) => {
    calls += 1;
    if (state < depth) store.set(state + 1);
  });

  store.set(1);

  assert.equal(store.get(), depth);
  assert.equal(calls, depth);
  assert.equal(store.getVersion(), depth);
});

test("合并: 落后的观察器只收到最新版本(折叠)", () => {
  const store = new Store(0);
  const first: number[] = [];
  const second: Array<[number, number]> = [];

  // first 先订阅,回调里连续改两次,使 second 落后多个版本
  store.subscribe((state, version) => {
    first.push(version);
    if (version === 1) {
      store.set(10);
      store.set(20);
    }
  });
  store.subscribe((state, version) => second.push([state, version]));

  store.set(1);

  // second 在同一轮里被折叠:只看到最终的 (20, v3),中间版本被跳过
  assert.deepEqual(second, [[20, 3]]);
  // first 看到的版本严格递增: v1, 然后是 v3(v2 被它自己的连续 set 折叠)
  assert.deepEqual(first, [1, 3]);
  for (let i = 1; i < first.length; i++) {
    assert.ok(first[i] > first[i - 1]);
  }
});

test("合并: batch 内多次修改只产生一次版本和一轮通知", () => {
  const store = new Store(0);
  const seen: Array<[number, number]> = [];
  store.subscribe((state, version) => seen.push([state, version]));

  store.batch(() => {
    store.set(1);
    store.set(2); // 后写覆盖先写
    store.update((n) => n + 1); // update 基于最新状态组合
  });

  assert.deepEqual(seen, [[3, 1]]);
  assert.equal(store.get(), 3);
  assert.equal(store.getVersion(), 1);
});

test("合并: 嵌套 batch 只在最外层提交一次", () => {
  const store = new Store(0);
  const seen: number[] = [];
  store.subscribe((state) => seen.push(state));

  store.batch(() => {
    store.set(1);
    store.batch(() => {
      store.set(2);
    });
    store.set(3);
  });

  assert.deepEqual(seen, [3]);
  assert.equal(store.getVersion(), 1);
});

test("合并: 通知回调中的 batch 同样合并为一次版本", () => {
  const store = new Store(0);
  const seen: Array<[number, number]> = [];
  store.subscribe((state, version) => {
    seen.push([state, version]);
    if (version === 1) {
      store.batch(() => {
        store.set(100);
        store.set(200);
      });
    }
  });

  store.set(1);

  assert.deepEqual(seen, [[1, 1], [200, 2]]);
});

test("退订: 退订后不再收到通知,且幂等", () => {
  const store = new Store(0);
  const seen: number[] = [];
  const unsub = store.subscribe((state) => seen.push(state));

  store.set(1);
  unsub();
  unsub(); // 幂等
  store.set(2);

  assert.deepEqual(seen, [1]);
  assert.equal(store.observerCount, 0);
});

test("退订: 回调中退订自己,立即生效", () => {
  const store = new Store(0);
  const seen: number[] = [];
  const unsub = store.subscribe((state, version) => {
    seen.push(state);
    if (version === 1) {
      unsub();
      store.set(2); // 触发新版本,但已退订,不应再收到
    }
  });

  store.set(1);

  assert.deepEqual(seen, [1]);
  assert.equal(store.get(), 2);
});

test("退订: 观察器 A 在回调中退订观察器 B,B 不再收到本次及后续通知", () => {
  const store = new Store(0);
  const seenB: number[] = [];
  const unsubB = store.subscribe((state) => seenB.push(state));
  store.subscribe(() => unsubB()); // 后订阅,本轮 B 已先收到

  store.set(1);
  store.set(2);

  assert.deepEqual(seenB, [1]);
});

test("异常隔离: 抛异常的观察器不影响其他观察器和后续派发", () => {
  const errors: unknown[] = [];
  const store = new Store(0, {
    onObserverError: (error) => errors.push(error),
  });
  const seen: number[] = [];
  store.subscribe(() => {
    throw new Error("boom");
  });
  store.subscribe((state) => seen.push(state));

  store.set(1);
  store.set(2);

  assert.deepEqual(seen, [1, 2]);
  assert.equal(errors.length, 2);
  assert.ok(errors[0] instanceof Error);
  assert.equal(store.get(), 2);
});

test("异常隔离: 观察器异常不重试,版本照常推进", () => {
  const store = new Store(0, { onObserverError: () => {} });
  let calls = 0;
  store.subscribe(() => {
    calls += 1;
    throw new Error("always fails");
  });

  store.set(1);
  store.set(2);

  assert.equal(calls, 2); // 每个版本各调用一次,不重试
  assert.equal(store.getVersion(), 2);
});

test("新增观察器: 通知期间新增的观察器不收当前版本,只收未来版本", () => {
  const store = new Store(0);
  const late: Array<[number, number]> = [];
  store.subscribe((state, version) => {
    if (version === 1) {
      store.subscribe((s, v) => late.push([s, v]));
      store.set(2);
    }
  });

  store.set(1);

  // 新观察器订阅时版本已是 1,不回放 v1;只收到 v2
  assert.deepEqual(late, [[2, 2]]);
});

test("新增观察器: 订阅时版本为当前版本,不回放历史", () => {
  const store = new Store(0);
  store.set(1);
  store.set(2);

  const seen: Array<[number, number]> = [];
  store.subscribe((s, v) => seen.push([s, v]));

  store.set(3);
  assert.deepEqual(seen, [[3, 3]]);
});

test("防护: 无条件修改的观察器触发 maxDispatchRounds", () => {
  const store = new Store(0, { maxDispatchRounds: 100, onObserverError: () => {} });
  store.subscribe((state) => store.set(state + 1));

  assert.throws(() => store.set(1), RangeError);
});

test("update: 基于最新状态组合", () => {
  const store = new Store(1);
  const seen: number[] = [];
  store.subscribe((s) => seen.push(s));

  store.update((n) => n * 10);
  store.update((n) => n + 5);

  assert.equal(store.get(), 15);
  assert.deepEqual(seen, [10, 15]);
});
