import { Store, set, update, tx } from './store.ts';
import { Status, initialState, runScenario, type AppState } from './scenario.ts';

const line = (title: string) => console.log(`\n=== ${title} ===`);

line('1. 同一调度周期两次 x=>x+1，合并一次提交（+2 而非 +1）');
{
  const store = new Store<AppState>(initialState);
  let notifies = 0;
  store.subscribe((s) => {
    notifies++;
    console.log(`  通知 #${notifies}: count=${s.count}`);
  });
  store.batch(() => {
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
    console.log(`  批次进行中 getState().count=${store.getState().count}（读者看不到中间态）`);
  });
  console.log(`  结果: count=${store.getState().count}, 通知次数=${notifies}`);
}

line('2. 枚举赋值 / 函数更新交错，次序保留');
{
  const store = new Store<AppState>(initialState);
  store.batch(() => {
    store.dispatch(set<AppState, 'status'>('status', Status.Loading));
    store.dispatch(update<AppState, 'status'>('status', (p) => (p === Status.Loading ? Status.Success : Status.Failed)));
    store.dispatch(set<AppState, 'count'>('count', 10));
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 5));
  });
  console.log(`  结果: status=${store.getState().status}, count=${store.getState().count}`);
}

line('3. 条件更新 + 跨字段不变量 + 读后再写（失败批次回滚）');
{
  const store = new Store<AppState>(initialState);
  store.addInvariant((s) => {
    if (s.balance < 0) throw new Error('invariant: balance must be >= 0');
  });
  store.batch(() => {
    store.dispatch(tx<AppState>((d) => {
      if (d.balance >= 30) d.balance -= 30;
    }));
    store.dispatch(tx<AppState>((d) => {
      d.lastRead = d.balance;
    }));
  });
  console.log(`  扣款后: balance=${store.getState().balance}, lastRead=${store.getState().lastRead}`);
  try {
    store.batch(() => {
      store.dispatch(set<AppState, 'balance'>('balance', -5));
      store.dispatch(set<AppState, 'count'>('count', 999));
    });
  } catch (err) {
    console.log(`  失败批次已回滚: ${(err as Error).message}`);
  }
  console.log(`  回滚后: balance=${store.getState().balance}, count=${store.getState().count}（无部分值）`);
}

line('4. 取消操作不撤销后续独立更新；操作 ID 重试幂等');
{
  const store = new Store<AppState>(initialState);
  store.batch(() => {
    store.dispatch(set<AppState, 'balance'>('balance', 0, { id: 'a' }));
    store.dispatch(set<AppState, 'balance'>('balance', 55, { id: 'b' }));
    console.log(`  cancel('a') 移除 ${store.cancel('a')} 个排队操作`);
  });
  console.log(`  结果: balance=${store.getState().balance}（后续独立更新保留）`);
  const first = store.dispatch(update<AppState, 'count'>('count', (x) => x + 100, { id: 'retry' }));
  const retry = store.dispatch(update<AppState, 'count'>('count', (x) => x + 100, { id: 'retry' }));
  console.log(`  首次 dispatch=${first}, 重试 dispatch=${retry}, count=${store.getState().count}（只应用一次）`);
}

line('5. 嵌套批次只最外层通知；可回放重建');
{
  const store = new Store<AppState>(initialState);
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
  const replayed = store.rebuild();
  console.log(`  count=${store.getState().count}, 通知次数=${notifies}`);
  console.log(`  回放重建一致: ${JSON.stringify(replayed) === JSON.stringify(store.getState())}`);
}

line('6. 合并优化 vs 未合并执行：结果 / 错误 / 副作用次数一致');
{
  const merged = runScenario('merged');
  const unmerged = runScenario('unmerged');
  console.log(`  merged   终态: ${JSON.stringify(merged.state)}`);
  console.log(`  unmerged 终态: ${JSON.stringify(unmerged.state)}`);
  console.log(`  结果一致: ${JSON.stringify(merged.state) === JSON.stringify(unmerged.state)}`);
  console.log(`  错误一致: ${JSON.stringify(merged.errors) === JSON.stringify(unmerged.errors)} (${merged.errors})`);
  console.log(`  副作用次数一致: ${JSON.stringify(merged.sideEffects) === JSON.stringify(unmerged.sideEffects)} (${JSON.stringify(merged.sideEffects)})`);
  console.log(`  通知次数: merged=${merged.notifyCount}, unmerged=${unmerged.notifyCount}`);
}
