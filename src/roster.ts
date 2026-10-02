/**
 * 领域层：医生值班表。
 * 业务不变量以声明式谓词表达，与业务流程解耦。
 */
import { TransactionalStore, Transaction } from './store.ts';

export const ROSTER_TABLE = 'duty_roster';

export interface DutyRow extends Record<string, unknown> {
  doctorId: string;
  name: string;
  onDuty: boolean;
}

/** 声明式约束：值班表至少有 1 人在岗 */
export function registerRosterInvariants(store: TransactionalStore): void {
  store.defineInvariant({
    name: 'at-least-one-doctor-on-duty',
    tables: [ROSTER_TABLE],
    description: '医生值班表至少有 1 人值班',
    assert: (view) => view.count(ROSTER_TABLE, (r) => r.onDuty === true) >= 1,
  });
}

export function seedRoster(
  store: TransactionalStore,
  doctors: { doctorId: string; name: string }[],
): Promise<void> {
  return store.transact((tx) => {
    for (const d of doctors) {
      tx.put(ROSTER_TABLE, d.doctorId, { ...d, onDuty: true } satisfies DutyRow);
    }
  });
}

/**
 * 医生申请请假：读取当前值班表（看到谁在岗），然后把自己标记为休息。
 * 关键点：读与写之间存在 await，为并发交叉留出真实窗口。
 */
export async function requestLeave(
  store: TransactionalStore,
  doctorId: string,
  interleave: () => Promise<void>,
): Promise<Transaction> {
  const tx = store.begin();
  const onDuty = tx.scan(ROSTER_TABLE, (r) => r.onDuty === true);
  console.log(
    `  [${doctorId}] 读到在岗名单: ${onDuty.map((r) => r.doctorId).join(', ')}`,
  );
  // 让出事件循环，模拟业务处理耗时 —— 另一事务可在此窗口内并发执行
  await interleave();
  const me = tx.get(ROSTER_TABLE, doctorId);
  if (!me) throw new Error(`医生 ${doctorId} 不在值班表中`);
  tx.put(ROSTER_TABLE, doctorId, { ...me, onDuty: false });
  await tx.commit();
  return tx;
}

export function onDutyNow(store: TransactionalStore): string[] {
  const tx = store.begin();
  const ids = tx.scan(ROSTER_TABLE, (r) => r.onDuty === true).map((r) => r.doctorId as string);
  tx.rollback();
  return ids;
}
