import test from 'node:test';
import assert from 'node:assert/strict';
import { Store, set, update, tx } from '../src/store.ts';
import { Status, initialState, runScenario, type AppState } from '../src/scenario.ts';

const makeStore = (mode: 'merged' | 'unmerged' = 'merged') =>
  new Store<AppState>(initialState, { mode });

test('同一调度周期内两次 x=>x+1 合并提交，结果 +2 且只通知一次', () => {
  const store = makeStore();
  let notifies = 0;
  store.subscribe(() => notifies++);
  store.batch(() => {
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
  });
  assert.equal(store.getState().count, 2);
  assert.equal(notifies, 1);
});

test('直接赋值与函数更新次序保留', () => {
  const store = makeStore();
  store.batch(() => {
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
    store.dispatch(set<AppState, 'count'>('count', 10));
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 5));
  });
  assert.equal(store.getState().count, 15);

  const store2 = makeStore();
  store2.batch(() => {
    store2.dispatch(set<AppState, 'count'>('count', 10));
    store2.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
  });
  assert.equal(store2.getState().count, 11);
});

test('有限状态枚举：赋值与函数更新交错按序求值', () => {
  const store = makeStore();
  store.batch(() => {
    store.dispatch(set<AppState, 'status'>('status', Status.Loading));
    store.dispatch(update<AppState, 'status'>('status', (p) =>
      p === Status.Loading ? Status.Success : Status.Failed,
    ));
  });
  assert.equal(store.getState().status, Status.Success);

  store.batch(() => {
    store.dispatch(update<AppState, 'status'>('status', () => Status.Loading));
    store.dispatch(set<AppState, 'status'>('status', Status.Failed));
  });
  assert.equal(store.getState().status, Status.Failed);
});

test('条件更新与读后再写在事务草稿上按序求值', () => {
  const store = makeStore();
  store.batch(() => {
    store.dispatch(tx<AppState>((d) => {
      if (d.balance >= 30) d.balance -= 30;
    }));
    store.dispatch(tx<AppState>((d) => {
      d.lastRead = d.balance;
    }));
  });
  assert.equal(store.getState().balance, 70);
  assert.equal(store.getState().lastRead, 70);
});

test('跨字段不变量违反时整批回滚，不留部分值', () => {
  const store = makeStore();
  store.addInvariant((s) => {
    if (s.balance < 0) throw new Error('invariant: balance must be >= 0');
  });
  const before = store.getState();
  assert.throws(
    () =>
      store.batch(() => {
        store.dispatch(set<AppState, 'balance'>('balance', -1));
        store.dispatch(set<AppState, 'count'>('count', 999));
      }),
    /balance must be >= 0/,
  );
  assert.deepEqual(store.getState(), before);
  assert.equal(store.getLog().length, 0);
});

test('批次回调抛错时丢弃全部排队操作', () => {
  const store = makeStore();
  assert.throws(() =>
    store.batch(() => {
      store.dispatch(set<AppState, 'count'>('count', 42));
      throw new Error('boom');
    }),
  );
  assert.equal(store.getState().count, 0);
  assert.equal(store.getLog().length, 0);
});

test('取消某操作不撤销后续独立更新', () => {
  const store = makeStore();
  store.batch(() => {
    store.dispatch(set<AppState, 'balance'>('balance', 0, { id: 'a' }));
    store.dispatch(set<AppState, 'balance'>('balance', 55, { id: 'b' }));
    assert.equal(store.cancel('a'), 1);
  });
  assert.equal(store.getState().balance, 55);
  // 已提交的操作无法被取消，也不影响后续状态
  assert.equal(store.cancel('b'), 0);
  assert.equal(store.getState().balance, 55);
});

test('操作 ID 重试幂等：排队中与已提交后重试均被跳过', () => {
  const store = makeStore();
  store.batch(() => {
    assert.equal(store.dispatch(update<AppState, 'count'>('count', (x) => x + 1, { id: 'op' })), true);
    assert.equal(store.dispatch(update<AppState, 'count'>('count', (x) => x + 1, { id: 'op' })), false);
  });
  assert.equal(store.getState().count, 1);
  assert.equal(store.dispatch(update<AppState, 'count'>('count', (x) => x + 1, { id: 'op' })), false);
  assert.equal(store.getState().count, 1);
});

test('嵌套批次只在最外层通知一次', () => {
  const store = makeStore();
  let notifies = 0;
  store.subscribe(() => notifies++);
  store.batch(() => {
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
    store.batch(() => {
      store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
      store.batch(() => {
        store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
      });
    });
  });
  assert.equal(store.getState().count, 3);
  assert.equal(notifies, 1);
});

test('读者看不到事务中间态', () => {
  const store = makeStore();
  const seen: number[] = [];
  store.subscribe((s) => seen.push(s.count));
  store.batch(() => {
    store.dispatch(set<AppState, 'count'>('count', 1));
    assert.equal(store.getState().count, 0); // 批次内读到的仍是提交前快照
    store.dispatch(set<AppState, 'count'>('count', 2));
  });
  assert.deepEqual(seen, [2]); // 订阅者只看到最终提交值
});

test('状态操作可回放：rebuild 与当前状态一致', () => {
  const store = makeStore();
  store.dispatch(set<AppState, 'status'>('status', Status.Loading));
  store.batch(() => {
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 7));
    store.dispatch(tx<AppState>((d) => {
      d.balance -= 20;
    }));
  });
  assert.deepEqual(store.rebuild(), store.getState());
  assert.equal(store.getLog().length, 3);
});

test('合并优化与未合并执行：结果、错误、副作用次数一致', () => {
  const merged = runScenario('merged');
  const unmerged = runScenario('unmerged');
  assert.deepEqual(merged.state, unmerged.state);
  assert.deepEqual(merged.errors, unmerged.errors);
  assert.deepEqual(merged.sideEffects, unmerged.sideEffects);
  assert.equal(merged.notifyCount, unmerged.notifyCount);
});

test('场景终态符合手工推演', () => {
  const { state, errors } = runScenario('merged');
  // count: +1+1 → *2 → set 10 → +5 → +100(幂等一次) → +1+1 = 117
  assert.equal(state.count, 117);
  // balance: 100 -30 → 55（取消 0 赋值）= 55；失败批次已回滚
  assert.equal(state.balance, 55);
  assert.equal(state.lastRead, 70);
  assert.equal(state.status, Status.Success);
  assert.deepEqual(errors, ['invariant: balance must be >= 0']);
});
