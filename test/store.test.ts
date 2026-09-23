import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/store.ts';

interface CounterState extends Record<string, unknown> {
  count: number;
  tag: string;
}

function makeStore() {
  const store = new Store<CounterState>({ count: 0, tag: 'init' });
  store.defineOp('inc', (s, n: number = 1) => ({ ...s, count: s.count + n }));
  store.defineOp('setTag', (s, tag: string) => ({ ...s, tag }));
  return store;
}

test('事务原子性：读者看不到中间态，监听器只通知一次', () => {
  const store = makeStore();
  const seen: number[] = [];
  store.subscribe((s) => seen.push(s.count));

  let insideTx: number | null = null;
  store.transact(() => {
    store.run('inc');
    store.run('inc');
    store.run('inc');
    insideTx = store.getState().count; // 事务中读取
  });

  assert.equal(insideTx, 0, '事务内 getState 应返回提交前状态');
  assert.equal(store.getState().count, 3);
  assert.deepEqual(seen, [3], '监听器只应收到一次最终状态');
});

test('事务抛错时整体回滚，无部分提交', () => {
  const store = makeStore();
  store.dispatch('inc');
  assert.throws(() =>
    store.transact(() => {
      store.run('inc');
      throw new Error('boom');
    }),
  );
  assert.equal(store.getState().count, 1);
  assert.equal(store.history('main').length, 1);
});

test('回到历史只重放纯状态变更，副作用不重复执行', () => {
  const store = makeStore();
  let effectCalls = 0;
  store.defineEffect('api', () => {
    effectCalls++;
    return { ok: true, seq: effectCalls };
  });

  for (let i = 0; i < 3; i++) {
    store.transact(() => {
      store.run('inc');
      store.emit('api', 'sync', i);
    });
  }
  assert.equal(effectCalls, 3);

  const first = store.history('main')[0].id;
  store.checkout(first);
  assert.equal(store.mode, 'replay');
  assert.equal(store.getState().count, 1, '历史状态由纯操作重放得到');
  assert.equal(effectCalls, 3, '回放不得重复执行副作用');

  store.backToLive();
  assert.equal(store.mode, 'live');
  assert.equal(store.getState().count, 3);
  assert.equal(effectCalls, 3);
});

test('回看提交记录不发送模拟请求，副作用结果来自提交日志', () => {
  const store = makeStore();
  let effectCalls = 0;
  store.defineEffect('api', (x: number) => {
    effectCalls++;
    return { seq: effectCalls, echo: x };
  });
  store.transact(() => {
    store.run('inc', 5);
    store.emit('api', 42);
  });

  const [commit] = store.history('main');
  const reviewed = store.getCommit(commit.id)!;
  assert.deepEqual(reviewed.effects[0].result, { seq: 1, echo: 42 });
  assert.equal(effectCalls, 1, '查看历史不得触发副作用');
});

test('派生值按历史依赖重算：无关依赖变化不触发重算', () => {
  const store = makeStore();
  store.defineDerived('double', ['count'], (s) => s.count * 2);
  store.defineDerived('tagLen', ['tag'], (s) => s.tag.length);

  assert.equal(store.derived('double'), 0);
  assert.equal(store.derived('tagLen'), 4);
  assert.equal(store.recomputeCount('double'), 1);
  assert.equal(store.recomputeCount('tagLen'), 1);

  store.dispatch('setTag', 'hello');
  assert.equal(store.derived('tagLen'), 5);
  assert.equal(store.recomputeCount('tagLen'), 2, 'tag 变化应重算 tagLen');
  assert.equal(store.derived('double'), 0);
  assert.equal(store.recomputeCount('double'), 1, 'count 未变，double 不应重算');

  store.dispatch('inc', 10);
  assert.equal(store.derived('double'), 20);
  const before = store.recomputeCount('double');
  const firstCommit = store.history('main')[0].id;
  store.checkout(firstCommit);
  assert.equal(store.derived('double'), 0, '派生值按历史状态计算');
  assert.ok(store.recomputeCount('double') > before, '历史依赖变化触发重算');
});

test('在历史提交上产生新操作会分叉，且不覆盖原实时历史', () => {
  const store = makeStore();
  store.dispatch('inc');
  store.dispatch('inc');
  store.dispatch('inc');
  const mainBefore = store.history('main').map((c) => c.id);
  const second = mainBefore[1];

  store.checkout(second);
  store.dispatch('setTag', 'forked'); // 在历史点上提交 -> 自动分叉

  assert.equal(store.branch, 'fork-1');
  assert.deepEqual(
    store.history('main').map((c) => c.id),
    mainBefore,
    'main 历史必须保持不变',
  );
  const fork = store.history('fork-1');
  assert.equal(fork.length, 3, 'fork 包含共享前缀 + 新提交');
  assert.equal(fork[2].parent, second, 'fork 新提交的父节点是被检出的历史提交');
  assert.equal(store.getState().tag, 'forked');
  assert.equal(store.getState().count, 2, 'fork 状态从历史点继续演化');
});

test('切回实时后恢复新操作，两条分支历史均完整', () => {
  const store = makeStore();
  store.dispatch('inc');
  store.dispatch('inc');
  const first = store.history('main')[0].id;

  store.checkout(first);
  store.dispatch('setTag', 'on-fork');
  store.switchBranch('main');

  assert.equal(store.mode, 'live');
  assert.equal(store.branch, 'main');
  assert.equal(store.getState().count, 2);

  store.dispatch('inc', 100); // 实时分支上的新操作
  assert.equal(store.getState().count, 102);

  const main = store.history('main');
  const fork = store.history('fork-1');
  assert.equal(main.length, 3);
  assert.equal(fork.length, 2);
  assert.equal(main[0].id, fork[0].id, '两分支共享历史前缀');
  assert.notEqual(main[1].id, fork[1].id, '分叉后各自演进');
});

test('状态操作可回放：同一操作序列重放得到一致状态', () => {
  const store = makeStore();
  store.dispatch('inc', 7);
  store.dispatch('setTag', 'a');
  store.dispatch('inc', 3);

  const tip = store.head!;
  store.checkout(tip);
  assert.deepEqual(store.getState(), { count: 10, tag: 'a' });

  // 再回放一次（幂等）
  store.checkout(tip);
  assert.deepEqual(store.getState(), { count: 10, tag: 'a' });
});
