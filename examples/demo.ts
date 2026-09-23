/**
 * 运行样例：模拟"搜索框 + 共享用户数据"场景。
 * 运行：node examples/demo.ts   （Node >= 22.6，推荐 >= 23.6）
 */
import { Store, atom, derived } from '../src/index.ts';
import type { DerivedState } from '../src/index.ts';

const sleep = (ms: number, signal?: AbortSignal): Promise<void> =>
  new Promise((resolve, reject) => {
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener('abort', () => {
      clearTimeout(timer);
      reject(new DOMException('aborted', 'AbortError'));
    }, { once: true });
  });

const store = new Store();

// ---- 场景 1：慢请求后快请求，只显示快结果 ----
const query = atom(store, 'query', '初始');

store.subscribe((snapshot, changed) => {
  if (changed.has('search')) {
    const s = snapshot.search as DerivedState<string>;
    console.log(`[搜索] 状态 -> ${s.status}${s.status === 'success' ? ` (${s.value})` : ''}`);
  }
});

derived(store, 'search', async (get, { signal }) => {
  const q = get(query);
  const latency = q === '慢查询' ? 150 : 20; // 模拟不同延迟的接口
  await sleep(latency, signal);
  return `"${q}" 的搜索结果`;
});

await sleep(30);
console.log('--- 先发起慢查询，立刻改发快查询 ---');
query.set('慢查询');
await sleep(10);
query.set('快查询');
await sleep(200);
console.log('最终快照 search =', JSON.stringify(store.snapshot().search));

// ---- 场景 2：共享依赖不重复执行 ----
console.log('\n--- 共享依赖：两个派生共用同一个异步任务 ---');
let fetchCount = 0;
const user = derived(store, 'user', async () => {
  fetchCount += 1;
  await sleep(20);
  return { name: 'Ada' };
});
derived(store, 'greeting', async (get) => `你好，${get(user)?.name}`);
derived(store, 'avatar', async (get) => `${get(user)?.name} 的头像`);
await sleep(60);
console.log(`user 任务执行次数 = ${fetchCount}（应为 1）`);
console.log('greeting =', JSON.stringify((store.snapshot().greeting as DerivedState<string>).value));
console.log('avatar   =', JSON.stringify((store.snapshot().avatar as DerivedState<string>).value));

// ---- 场景 3：状态操作回放 ----
console.log('\n--- 回放：用操作日志在全新 Store 上重建状态 ---');
const replayed = Store.replay(JSON.parse(JSON.stringify(store.log)));
const same = JSON.stringify(replayed.snapshot()) === JSON.stringify(store.snapshot());
console.log(`日志共 ${store.log.length} 条操作，回放结果一致：${same}`);
