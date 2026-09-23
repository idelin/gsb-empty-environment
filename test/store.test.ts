import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Store,
  LoopBudgetExceededError,
  NotificationAggregateError,
  type Notification,
} from '../src/store.ts';

interface Counter {
  count: number;
  tag: string;
  log: string[];
}

const init = (): Counter => ({ count: 0, tag: 'a', log: [] });

function capture(fn: () => void): unknown {
  try {
    fn();
  } catch (error) {
    return error;
  }
  throw new Error('expected function to throw');
}

test('提交后按确定顺序（订阅 id 升序）通知，且通知发生在提交之后', () => {
  const store = new Store<Counter>({ initialState: init() });
  const order: string[] = [];
  store.subscribe((s) => s.count, () => void order.push('first'));
  store.subscribe((s) => s.count, () => void order.push('second'));
  store.subscribe((s) => s.count, (n) => {
    // 通知发生时提交已生效：读到的是已提交状态
    assert.equal(store.getState().count, 1);
    assert.equal(n.version, 1);
    order.push('third');
  });
  store.commit((d) => void (d.count += 1));
  assert.deepEqual(order, ['first', 'second', 'third']);
});

test('通知回调内再写进入下一轮，版本按轮递增', () => {
  const store = new Store<Counter>({ initialState: init() });
  const seen: Array<{ round: number; version: number; count: number }> = [];
  let chained = false;
  store.subscribe((s) => s.count, (n) => {
    seen.push({ round: n.round, version: n.version, count: n.value });
    if (!chained) {
      chained = true;
      store.commit('reentrant', (d) => void (d.count += 10)); // 进入下一轮
    }
  });
  store.commit('first', (d) => void (d.count += 1));
  assert.equal(store.version, 2);
  assert.deepEqual(seen, [
    { round: 1, version: 1, count: 1 },
    { round: 2, version: 2, count: 11 },
  ]);
});

test('通知期间取消/新增订阅不影响当前快照迭代', () => {
  const store = new Store<Counter>({ initialState: init() });
  const hits: Record<string, number[]> = { a: [], b: [], late: [] };
  let subB!: { unsubscribe(): void };
  store.subscribe((s) => s.count, (n) => {
    hits.a.push(n.round);
    if (n.round === 1) {
      subB.unsubscribe(); // 取消：本轮 b 仍应收到
      store.subscribe((s) => s.count, (m) => void hits.late.push(m.round)); // 新增：本轮不应收到
    }
  });
  subB = store.subscribe((s) => s.count, (n) => void hits.b.push(n.round));
  store.commit((d) => void (d.count += 1));
  store.commit((d) => void (d.count += 1));
  assert.deepEqual(hits.a, [1, 2]);
  assert.deepEqual(hits.b, [1]); // 第一轮快照仍包含 b，第二轮起消失
  assert.deepEqual(hits.late, [2]); // 新增订阅从下一轮才开始
});

test('自取消只影响后轮：当前轮通知照发', () => {
  const store = new Store<Counter>({ initialState: init() });
  const rounds: number[] = [];
  const handle = store.subscribe((s) => s.count, (n) => {
    rounds.push(n.round);
    handle.unsubscribe();
  });
  store.commit((d) => void (d.count += 1));
  store.commit((d) => void (d.count += 1));
  assert.deepEqual(rounds, [1]);
  assert.equal(handle.isActive(), false);
});

test('选择器可动态更换依赖，相等策略可动态更换', () => {
  const store = new Store<Counter>({ initialState: init() });
  const seen: unknown[] = [];
  const sub = store.subscribe((s) => s.count, (n) => void seen.push(n.value));
  store.commit((d) => void (d.count += 1)); // count: 0 -> 1
  assert.deepEqual(seen, [1]);

  // 动态换依赖：改盯 tag，重定基线，不产生伪通知
  sub.setSelector((s: Counter) => s.tag);
  store.commit((d) => void (d.count += 1)); // 只动 count
  assert.deepEqual(seen, [1]);
  store.commit((d) => void (d.tag = 'b'));
  assert.deepEqual(seen, [1, 'b']);

  // 动态换相等策略：长度相等即视为不变
  sub.setSelector((s: Counter) => s.tag);
  sub.setEquality((x: string, y: string) => x.length === y.length);
  store.commit((d) => void (d.tag = 'c')); // 等长，抑制
  store.commit((d) => void (d.tag = 'longer')); // 变长，通知
  assert.deepEqual(seen, [1, 'b', 'longer']);
});

test('通知只针对已提交版本：失败事务原子回滚，读者看不到中间态', () => {
  const store = new Store<Counter>({ initialState: init() });
  const notified: number[] = [];
  store.subscribe((s) => s.count, (n) => void notified.push(n.value));

  // 事务内读取：只能看到上一个已提交状态
  const insideTx: number[] = [];
  store.commit((d) => {
    d.count = 100; // 中间态
    insideTx.push(store.getState().count);
    d.count = 7;
  });
  assert.deepEqual(insideTx, [0]);
  assert.deepEqual(notified, [7]); // 只通知最终提交值，绝无中间态 100

  // 失败事务：版本不变、状态不变、无通知
  const before = store.getState();
  assert.throws(() =>
    store.commit((d) => {
      d.count = 999;
      throw new Error('boom');
    }),
  );
  assert.equal(store.version, 1);
  assert.equal(store.getState(), before); // 引用不变
  assert.equal(store.getState().count, 7);
  assert.deepEqual(notified, [7]);

  // 嵌套事务被拒绝
  assert.throws(() => store.commit(() => void store.commit(() => {})), /nested commit/);
});

test('订阅者抛错不阻断其他通知，完成后聚合报告', () => {
  const store = new Store<Counter>({ initialState: init() });
  const good: number[] = [];
  store.subscribe((s) => s.count, () => {
    throw new Error('sub-1 failed');
  });
  store.subscribe((s) => s.count, (n) => void good.push(n.value));
  store.subscribe((s) => s.count, () => {
    throw new Error('sub-3 failed');
  });

  const thrown = capture(() => store.commit((d) => void (d.count += 1)));
  assert.ok(thrown instanceof NotificationAggregateError);
  assert.equal(thrown.errors.length, 2);
  assert.deepEqual(thrown.errors.map((e) => e.subscriberId), [1, 3]);
  assert.deepEqual(good, [1]); // 好订阅者照常收到
  assert.equal(store.getState().count, 1); // 提交本身成功

  // 抛错的监听器基线已更新，不会被无限重试
  const again = capture(() => store.commit((d) => void (d.count += 1)));
  assert.ok(again instanceof NotificationAggregateError);
  assert.equal(again.errors.length, 2);
});

test('聚合错误可交给 onNotificationError 回调而非抛出', () => {
  const reports: NotificationAggregateError[] = [];
  const store = new Store<Counter>({
    initialState: init(),
    onNotificationError: (e) => void reports.push(e),
  });
  store.subscribe((s) => s.count, () => {
    throw new Error('x');
  });
  store.commit((d) => void (d.count += 1));
  assert.equal(reports.length, 1);
  assert.equal(reports[0].errors.length, 1);
});

test('背压合并：积存提交合并为一轮，附版本范围证据', () => {
  const store = new Store<Counter>({ initialState: init(), autoFlush: false });
  const notes: Notification<number>[] = [];
  store.subscribe((s) => s.count, (n) => void notes.push(n));
  store.commit('c1', (d) => void (d.count = 1));
  store.commit('c2', (d) => void (d.count = 2));
  store.commit('c3', (d) => void (d.count = 3));
  assert.equal(store.pendingCount, 3); // 背压积存证据

  const report = store.flush();
  assert.equal(notes.length, 1); // 合并为一轮一次通知
  const n = notes[0];
  assert.equal(n.fromVersion, 1);
  assert.equal(n.toVersion, 3);
  assert.equal(n.version, 3);
  assert.equal(n.mergedCommits, 3);
  assert.equal(n.value, 3); // 只投递最新值
  assert.deepEqual(
    report.rounds.map((r) => [r.fromVersion, r.toVersion, r.mergedCommits]),
    [[1, 3, 3]],
  );
});

test('递归更新受循环预算保护，错误附未投递版本证据', () => {
  const store = new Store<Counter>({ initialState: init(), maxRounds: 8 });
  store.subscribe((s) => s.count, () => {
    store.commit((d) => void (d.count += 1)); // 每轮都再写：无限递归
  });
  const err = capture(() => store.commit((d) => void (d.count += 1)));
  assert.ok(err instanceof LoopBudgetExceededError);
  assert.equal(err.maxRounds, 8);
  assert.ok(err.pendingFromVersion <= err.pendingToVersion);
  assert.ok(store.pendingCount > 0); // 未投递版本仍在队列中，证据可查
});

test('操作日志可回放：重建状态与当前已提交状态一致', () => {
  const store = new Store<Counter>({ initialState: init() });
  store.commit('inc', (d) => void (d.count += 1));
  store.commit('tag', (d) => void (d.tag = 'x'));
  store.commit('log', (d) => void d.log.push('hello'));
  assert.throws(() => store.commit(() => {
    throw new Error('discarded');
  }));
  assert.deepEqual(store.replay(), store.getState());
  assert.deepEqual(store.opLog().map((e) => [e.version, e.label]), [
    [1, 'inc'],
    [2, 'tag'],
    [3, 'log'],
  ]);
});

test('已提交状态深冻结，读者无法篡改', () => {
  const store = new Store<Counter>({ initialState: init() });
  store.commit((d) => void (d.count += 1));
  const state = store.getState();
  assert.ok(Object.isFrozen(state));
  assert.ok(Object.isFrozen(state.log));
  assert.throws(() => {
    (state as { count: number }).count = 42;
  }, TypeError);
  assert.equal(store.getState().count, 1);
});
