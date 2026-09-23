import { Store, set, update, tx, type CommitMode } from './store.ts';

/** 有限状态机（可擦除的枚举写法）。 */
export const Status = {
  Idle: 'idle',
  Loading: 'loading',
  Success: 'success',
  Failed: 'failed',
} as const;
export type Status = (typeof Status)[keyof typeof Status];

export interface AppState {
  count: number;
  balance: number;
  status: Status;
  lastRead: number;
}

export const initialState: AppState = {
  count: 0,
  balance: 100,
  status: Status.Idle,
  lastRead: 0,
};

export interface ScenarioResult {
  state: AppState;
  notifyCount: number;
  sideEffects: Record<string, number>;
  errors: string[];
}

/** 跨字段不变量：余额不能为负；success 状态下计数必须为正。 */
function invariants(store: Store<AppState>): void {
  store.addInvariant((s) => {
    if (s.balance < 0) throw new Error('invariant: balance must be >= 0');
  });
  store.addInvariant((s) => {
    if (s.status === Status.Success && s.count <= 0) {
      throw new Error('invariant: success requires count > 0');
    }
  });
}

/**
 * 代表性场景：函数更新连发、枚举赋值/函数更新交错、条件更新、
 * 读后再写、取消、幂等重试、嵌套批次、失败批次回滚。
 * 在 merged / unmerged 两种模式下执行，结果必须一致。
 */
export function runScenario(mode: CommitMode): ScenarioResult {
  const store = new Store<AppState>(initialState, { mode });
  invariants(store);

  const sideEffects: Record<string, number> = { inc: 0, double: 0, spend: 0 };
  const errors: string[] = [];
  let notifyCount = 0;
  store.subscribe(() => {
    notifyCount++;
  });

  // 1) 同一调度周期内两次 x => x + 1，合并提交后必须 +2。
  store.batch(() => {
    store.dispatch(update<AppState, 'count'>('count', (x) => {
      sideEffects.inc++;
      return x + 1;
    }));
    store.dispatch(update<AppState, 'count'>('count', (x) => {
      sideEffects.inc++;
      return x + 1;
    }));
  });

  // 2) 枚举赋值与函数更新交错，次序保留。
  store.batch(() => {
    store.dispatch(set<AppState, 'status'>('status', Status.Loading));
    store.dispatch(update<AppState, 'status'>('status', (prev) =>
      prev === Status.Loading ? Status.Success : Status.Failed,
    ));
    store.dispatch(update<AppState, 'count'>('count', (x) => {
      sideEffects.double++;
      return x * 2;
    }));
    store.dispatch(set<AppState, 'count'>('count', 10)); // 直接赋值覆盖前面的函数更新
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 5)); // 10 + 5 = 15
  });

  // 3) 条件更新 + 读后再写 + 跨字段写入，同一事务草稿按序求值。
  store.batch(() => {
    store.dispatch(tx<AppState>((d) => {
      if (d.count > 10) {
        sideEffects.spend++;
        d.balance -= 30; // 条件更新：count 足够才扣款
      }
    }));
    store.dispatch(tx<AppState>((d) => {
      d.lastRead = d.balance; // 读后再写：读取本批次内刚写入的 balance
    }));
  });

  // 4) 取消某操作不撤销后续独立更新。
  store.batch(() => {
    store.dispatch(set<AppState, 'balance'>('balance', 0, { id: 'op-cancel-me' }));
    store.dispatch(set<AppState, 'balance'>('balance', 55, { id: 'op-keep' }));
    store.cancel('op-cancel-me');
  });

  // 5) 操作 ID 重试幂等：第二次 dispatch 被跳过。
  store.dispatch(update<AppState, 'count'>('count', (x) => x + 100, { id: 'op-once' }));
  store.dispatch(update<AppState, 'count'>('count', (x) => x + 100, { id: 'op-once' }));

  // 6) 嵌套批次只在最外层通知一次。
  store.batch(() => {
    store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
    store.batch(() => {
      store.dispatch(update<AppState, 'count'>('count', (x) => x + 1));
    });
  });

  // 7) 失败批次：不变量违反，整批回滚，不留部分值。
  try {
    store.batch(() => {
      store.dispatch(set<AppState, 'balance'>('balance', -1));
      store.dispatch(set<AppState, 'count'>('count', 999));
    });
  } catch (err) {
    errors.push((err as Error).message);
  }

  return { state: store.getState(), notifyCount, sideEffects, errors };
}
