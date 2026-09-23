import {
  Store,
  LoopBudgetExceededError,
  NotificationAggregateError,
} from '../src/store.ts';

interface AppState {
  count: number;
  user: string;
}

const store = new Store<AppState>({
  initialState: { count: 0, user: 'anon' },
  maxRounds: 8,
  onNotificationError: (err) => {
    console.log(`\n[聚合错误报告] ${err.message}`);
    for (const e of err.errors) console.log(`  订阅者 #${e.subscriberId}: ${(e.error as Error).message}`);
  },
});

console.log('=== 1. 确定性顺序通知 + 通知内再写进入下一轮 ===');
let chained = false;
store.subscribe((s) => s.count, (n) => {
  console.log(`  [观察者A] round=${n.round} v${n.version} count=${n.value}`);
  if (!chained) {
    chained = true;
    console.log('  [观察者A] 在通知内再写 -> 进入下一轮');
    store.commit('chained', (d) => void (d.count += 100));
  }
});
store.subscribe((s) => s.count, (n) => {
  console.log(`  [观察者B] round=${n.round} v${n.version} count=${n.value}`);
});
store.commit('inc', (d) => void (d.count += 1));

console.log('\n=== 2. 订阅者抛错聚合，不阻断他人 ===');
store.subscribe((s) => s.user, () => {
  throw new Error('渲染失败');
});
store.commit('rename', (d) => void (d.user = 'delin'));

console.log('\n=== 3. 动态更换选择器依赖 ===');
const sub = store.subscribe((s) => s.user, (n) => {
  console.log(`  [观察者C] 选中值 -> ${n.value}`);
});
store.commit('rename2', (d) => void (d.user = 'pairwise'));
sub.setSelector((s: AppState) => s.count);
store.commit('inc2', (d) => void (d.count += 1));
console.log(`  (换依赖后观察者C改盯 count，不再随 user 变化)`);

console.log('\n=== 4. 背压合并的版本范围证据 ===');
const batch = new Store<AppState>({ initialState: { count: 0, user: 'x' }, autoFlush: false });
batch.subscribe((s) => s.count, (n) => {
  console.log(
    `  合并通知: 版本范围 v${n.fromVersion}..v${n.toVersion}, 合并提交数=${n.mergedCommits}, 最新值=${n.value}`,
  );
});
for (let i = 1; i <= 5; i++) batch.commit(`burst-${i}`, (d) => void (d.count = i));
console.log(`  flush 前积存版本数: ${batch.pendingCount}`);
batch.flush();

console.log('\n=== 5. 递归更新受循环预算保护 ===');
const loop = new Store<AppState>({ initialState: { count: 0, user: 'x' }, maxRounds: 4 });
loop.subscribe((s) => s.count, () => loop.commit((d) => void (d.count += 1)));
try {
  loop.commit((d) => void (d.count += 1));
} catch (error) {
  const e = error as LoopBudgetExceededError;
  console.log(`  捕获 ${e.name}: 预算=${e.maxRounds} 轮, 未投递版本 v${e.pendingFromVersion}..v${e.pendingToVersion}`);
}

console.log('\n=== 6. 操作回放核验 ===');
console.log(`  replay 与当前状态一致: ${JSON.stringify(store.replay()) === JSON.stringify(store.getState())}`);
console.log(`  操作日志: ${JSON.stringify(store.opLog())}`);

// 让 NotificationAggregateError 类型在示例中显式可见
console.log(`\n(聚合错误类型: ${NotificationAggregateError.name})`);
