import { RefreshCoordinator, type SourcePayload } from './store.ts';

const SOURCES = ['orders', 'users', 'inventory'];
const c = new RefreshCoordinator(SOURCES);

const show = (label: string) => {
  const v = c.getView();
  console.log(`${label}\n  读者视图 -> version=${v.version} data=${JSON.stringify(v.data)}`);
};

console.log('=== 场景 1：三源齐刷到 v1，原子发布 ===');
await c.refreshAll(1, {
  orders: async () => ({ version: 1, data: ['o1'] }),
  users: async () => ({ version: 1, data: ['u1'] }),
  inventory: async () => ({ version: 1, data: ['i1'] }),
});
show('round 1 提交后');

console.log('\n=== 场景 2：刷 v2 时 users 报旧版本、inventory 抛错 -> 保持旧视图 ===');
const s2 = await c.refreshAll(2, {
  orders: async () => ({ version: 2, data: ['o1', 'o2'] }),
  users: async () => ({ version: 1, data: ['u1'] }), // 旧数据，不能冒充
  inventory: async () => { throw new Error('upstream 503'); },
});
console.log(`  失败源报告 -> ${JSON.stringify([...c.getFailedSources()])}`);
show(`round 2 未提交 (committed=${s2.committed})`);

console.log('\n=== 场景 3：逐个重试失败源，补齐后恰好发布一次 ===');
await c.retrySource('users', async () => ({ version: 2, data: ['u1', 'u2'] }));
show('users 重试成功（inventory 仍缺，视图不变）');
const s3 = await c.retrySource('inventory', async () => ({ version: 2, data: ['i1', 'i2'] }));
show(`inventory 重试成功 (committed=${s3.committed})`);
const commits = c.getLog().filter((e) => e.type === 'Committed' && e.round === s3.round);
console.log(`  round ${s3.round} 的 Committed 事件数 = ${commits.length}（恰好一次）`);

console.log('\n=== 场景 4：过期轮次的迟到结果被围栏拒绝 ===');
const round3 = c.startRefresh(3);
c.reportSource(round3, 'orders', { version: 3, data: ['o3'] });
const stale = c.reportSource(s3.round, 'users', { version: 2, data: ['late'] } as SourcePayload);
console.log(`  上一轮(round ${s3.round})迟到的上报 -> ${JSON.stringify(stale)}`);
show('round 3 进行中（读者仍看到 v2，无中间态）');

console.log('\n=== 场景 5：事件日志回放，重建相同状态 ===');
const replayed = RefreshCoordinator.replay(SOURCES, c.getLog());
console.log(`  回放视图与实时视图一致 -> ${JSON.stringify(replayed.getView()) === JSON.stringify(c.getView())}`);
console.log(`\n完整事件日志（${c.getLog().length} 条）:`);
for (const e of c.getLog()) console.log(' ', JSON.stringify(e));
