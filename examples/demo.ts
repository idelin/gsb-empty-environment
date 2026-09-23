import {
  Store,
  delay,
  type CellState,
  type SnapshotEntry,
  type SourceCell,
} from '../src/store.ts';

// 模拟后端：cat 是慢请求(150ms)，dog 是快请求(20ms)
const latency: Record<string, number> = { cat: 150, dog: 20 };
let orderFetches = 0;

interface DemoRefs {
  query: SourceCell<string>;
  user: SourceCell<string>;
  first: SourceCell<string>;
  last: SourceCell<string>;
}

/** 注册全部单元；回放时复用同一构建函数。 */
function buildStore(store: Store): DemoRefs {
  const query = store.source('query', 'cat');
  const user = store.source('user', 'u-1');
  const first = store.source('first', 'Ada');
  const last = store.source('last', 'Byron');

  store.derived('search', [query], async (ctx, q) => {
    console.log(`    → 发起搜索请求: ${q}`);
    await delay(latency[q] ?? 30, ctx.signal);
    return `${q} 的搜索结果`;
  });

  store.shared('orders', [user], async (ctx, uid) => {
    orderFetches++;
    await delay(20, ctx.signal);
    return [`${uid} 的订单#1`, `${uid} 的订单#2`];
  });

  store.derived('fullName', [first, last], async (_ctx, f, l) => `${f} ${l}`);

  return { query, user, first, last };
}

function fmt(state: CellState<unknown>): string {
  if (state.status === 'pending') return 'pending(加载中…)';
  if (state.status === 'error') return `error(${String(state.error)})`;
  return `success(${JSON.stringify(state.value)})`;
}

async function main(): Promise<void> {
  const store = new Store();
  const { query, user, first, last } = buildStore(store);

  console.log('== 场景 1：先慢请求后快请求，只显示快结果 ==');
  const search = store.getCell('search')!;
  search.subscribe(s => console.log(`    UI: ${fmt(s)}`));
  await delay(30); // 慢请求 cat(150ms) 已在途中
  query.set('dog'); // 用户改输入：取消 cat，启动更快的 dog(20ms)
  await store.whenSettled();
  console.log(`    最终: ${fmt(search.get())}`);
  console.log('    （cat 的慢结果被代际拒绝，取消未产生 error 状态）\n');

  console.log('== 场景 2：共享依赖不重复执行同一任务 ==');
  const again = store.shared('orders', [user], async () => {
    throw new Error('不应被执行');
  });
  await store.whenSettled();
  console.log(`    两次 shared() 返回同一单元: ${again === store.getCell('shared:orders')}`);
  console.log(`    订单任务执行次数: ${orderFetches}（预期 1）`);
  console.log(`    订单: ${JSON.stringify(store.getCell('shared:orders')!.get().value)}\n`);

  console.log('== 场景 3：事务提交前读者看不到中间态 ==');
  const fullName = store.getCell('fullName')!;
  fullName.subscribe(s => console.log(`    UI: ${fmt(s)}`));
  await store.whenSettled();
  store.transaction(() => {
    first.set('Grace');
    last.set('Hopper');
  });
  await store.whenSettled();
  console.log('    （只有一次 pending→success，从未出现 "Grace Byron" 中间态）\n');

  console.log('== 场景 4：状态操作可回放 ==');
  const log = store.getReplayLog();
  console.log(`    命令日志: ${log.map(c => `${c.cell}←${JSON.stringify(c.value)}`).join('  ')}`);
  const replayed = Store.replay(log, buildStore);
  await replayed.whenSettled();
  const strip = (snap: SnapshotEntry[]) => snap.map(({ version, ...rest }) => rest);
  const same = JSON.stringify(strip(store.snapshot())) === JSON.stringify(strip(replayed.snapshot()));
  console.log(`    回放后最终状态一致: ${same}`);
}

main().catch(err => {
  console.error(err);
  process.exitCode = 1;
});
