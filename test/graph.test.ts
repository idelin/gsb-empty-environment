import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store, atom, derived } from '../src/index.ts';
import type { DerivedState } from '../src/index.ts';
import { sleep, waitFor } from './helpers.ts';

test('状态机：pending -> success', async () => {
  const store = new Store();
  const query = atom(store, 'query', 'hello');
  const statuses: string[] = [];
  store.subscribe((snapshot, changed) => {
    if (changed.has('result')) statuses.push((snapshot.result as DerivedState<string>).status);
  });

  derived(store, 'result', async (get, { signal }) => {
    await sleep(10, signal);
    return `结果:${get(query)}`;
  });

  const final = await waitFor<DerivedState<string>>(
    store,
    'result',
    (s) => s.status === 'success',
  );
  assert.deepEqual(statuses, ['pending', 'success']);
  assert.deepEqual(final, { status: 'success', value: '结果:hello' });
});

test('慢请求后快请求：只显示快结果，旧的慢结果按代际被拒绝', async () => {
  const store = new Store();
  const query = atom(store, 'query', '慢');
  const history: Array<DerivedState<string>> = [];
  store.subscribe((snapshot, changed) => {
    if (changed.has('result')) history.push(snapshot.result as DerivedState<string>);
  });

  derived(store, 'result', async (get, { signal }) => {
    const q = get(query);
    await sleep(q === '慢' ? 120 : 10, signal); // 慢任务 120ms，快任务 10ms
    return `结果:${q}`;
  });

  await sleep(20); // 让慢任务先跑起来
  query.set('快'); // 依赖变化：取消慢任务，启动快任务

  const final = await waitFor<DerivedState<string>>(
    store,
    'result',
    (s) => s.status === 'success',
  );
  await sleep(200); // 等慢任务（若未被取消/拒绝）也结束

  assert.deepEqual(final, { status: 'success', value: '结果:快' });
  const successes = history.filter((s) => s.status === 'success');
  assert.deepEqual(successes, [{ status: 'success', value: '结果:快' }], '慢结果从未出现');
});

test('取消不被当业务错误：被中止的任务不进入 error 状态', async () => {
  const store = new Store();
  const query = atom(store, 'query', '第一次');
  const statuses: string[] = [];
  let abortedSeen = 0;
  store.subscribe((snapshot, changed) => {
    if (changed.has('result')) statuses.push((snapshot.result as DerivedState<string>).status);
  });

  derived(store, 'result', async (get, { signal }) => {
    const q = get(query);
    try {
      await sleep(50, signal);
    } catch (err) {
      if ((err as DOMException).name === 'AbortError') abortedSeen += 1;
      throw err;
    }
    return `结果:${q}`;
  });

  await sleep(10);
  query.set('第二次'); // 取消第一代任务
  await waitFor<DerivedState<string>>(store, 'result', (s) => s.status === 'success');

  assert.equal(abortedSeen, 1, '旧任务收到了取消信号');
  assert.ok(!statuses.includes('error'), '取消从未进入 error 状态');
  assert.deepEqual(statuses, ['pending', 'pending', 'success']);
});

test('业务异常进入 error 状态，错误被序列化为纯数据', async () => {
  const store = new Store();
  const flag = atom(store, 'flag', false);
  derived(store, 'mayFail', async (get) => {
    if (!get(flag)) return 'ok';
    throw new TypeError('业务失败');
  });
  await waitFor<DerivedState<string>>(store, 'mayFail', (s) => s.status === 'success');

  flag.set(true);
  const errored = await waitFor<DerivedState<string>>(
    store,
    'mayFail',
    (s) => s.status === 'error',
  );
  assert.deepEqual(errored, {
    status: 'error',
    error: { name: 'TypeError', message: '业务失败' },
  });
  // error 状态也是纯数据，可回放
  const replayed = Store.replay(JSON.parse(JSON.stringify(store.log)));
  assert.deepEqual(replayed.snapshot(), store.snapshot());
});

test('共享依赖不重复执行：多个派生读同一异步节点，任务只跑一次', async () => {
  const store = new Store();
  let taskRuns = 0;
  const user = derived(store, 'user', async () => {
    taskRuns += 1;
    await sleep(10);
    return { name: 'Ada' };
  });

  const greeting = derived(store, 'greeting', async (get) => `你好，${get(user)?.name}`);
  const profile = derived(store, 'profile', async (get) => {
    const u = get(user);
    return u ? `${u.name} 的资料` : '加载中';
  });

  const [g, p] = await Promise.all([
    waitFor<DerivedState<string>>(store, 'greeting', (s) => s.status === 'success' && s.value === '你好，Ada'),
    waitFor<DerivedState<string>>(store, 'profile', (s) => s.status === 'success' && s.value === 'Ada 的资料'),
  ]);
  assert.equal(g.value, '你好，Ada');
  assert.equal(p.value, 'Ada 的资料');
  assert.equal(taskRuns, 1, '共享的异步任务只执行一次');
  assert.equal(greeting.state().status, 'success');
  assert.equal(profile.state().status, 'success');
});

test('dispose 取消在途任务，晚到结果被丢弃', async () => {
  const store = new Store();
  let aborted = false;
  const node = derived(store, 'temp', async (_get, { signal }) => {
    try {
      await sleep(50, signal);
    } catch {
      aborted = true;
      throw new Error('不应被记录');
    }
    return '太晚';
  });
  await sleep(10);
  node.dispose();
  await sleep(80);

  assert.ok(aborted, 'dispose 中止了在途任务');
  assert.deepEqual(node.state(), { status: 'pending' }, '晚到结果被丢弃，状态不被改写');
});
