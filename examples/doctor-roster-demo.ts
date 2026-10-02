/**
 * 运行样例：写倾斜（write skew）攻防演示。
 *
 * 场景：医生 A、B 同时值班。
 *   事务1：A 申请请假（读到 B 在岗，认为安全）
 *   事务2：B 同时申请请假（读到 A 在岗，认为安全）
 * 两事务各自看到的快照都满足"至少 1 人值班"，但若都提交，
 * 值班人数将从 2 跌为 0 —— 典型的写倾斜异常。
 *
 * 本系统在提交阶段重验声明式不变量 + 读集版本检测，
 * 精准中止其中一个事务，保证不变量成立。
 */
import { TransactionalStore } from '../src/store.ts';
import {
  registerRosterInvariants,
  seedRoster,
  requestLeave,
  onDutyNow,
} from '../src/roster.ts';

const tick = () => new Promise((resolve) => setImmediate(resolve));

async function main(): Promise<void> {
  const store = new TransactionalStore();
  registerRosterInvariants(store);
  await seedRoster(store, [
    { doctorId: 'A', name: '医生A' },
    { doctorId: 'B', name: '医生B' },
  ]);

  console.log(`初始在岗: ${onDutyNow(store).join(', ')}\n`);
  console.log('--- A 与 B 并发申请请假 ---');

  const [resultA, resultB] = await Promise.allSettled([
    requestLeave(store, 'A', tick),
    requestLeave(store, 'B', tick),
  ]);

  console.log('\n--- 提交结果 ---');
  for (const [id, r] of [['A', resultA], ['B', resultB]] as const) {
    if (r.status === 'fulfilled') {
      console.log(`  ${id} 请假事务: 提交成功`);
    } else {
      console.log(`  ${id} 请假事务: 已中止 -> ${(r.reason as Error).message}`);
    }
  }

  const remaining = onDutyNow(store);
  console.log(`\n最终在岗: ${remaining.join(', ')}`);
  console.log(
    remaining.length >= 1
      ? '✔ 不变量保持：值班表至少有 1 人值班'
      : '✘ 不变量被破坏！',
  );
}

main();
