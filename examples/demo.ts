import { OccEngine } from '../src/occ/engine.ts';
import { OccConflictError } from '../src/occ/errors.ts';
import { sleep } from '../src/util/sleep.ts';

// ---------- 场景 1：经典三阶段 + 重叠写冲突 -> 精准验证 + 干净回滚 ----------
const engine = new OccEngine<string, number>();
await engine.run((txn) => {
  txn.set('balance', 100);
  txn.set('bonus', 0);
});
console.log('== 场景 1：重叠写入触发验证失败 ==');
console.log('初始状态: balance=100, bonus=0');

const alice = engine.begin();
const bob = engine.begin();
console.log(`Alice 读取阶段: balance=${alice.get('balance')}`);
console.log(`Bob   读取阶段: balance=${bob.get('balance')}`);

alice.set('balance', 80);
alice.set('bonus', 5);
engine.commit(alice);
console.log('Alice 验证通过，批量提交: balance=80, bonus=5');

bob.set('balance', 60);
bob.set('bonus', 999);
try {
  engine.commit(bob);
} catch (err) {
  if (err instanceof OccConflictError) {
    console.log(`Bob 验证失败: ReadSet 中键 [${err.conflictingKeys.join(', ')}] 版本已过期，事务整体回滚`);
  }
}
console.log(
  `回滚后存储: balance=${engine.store.getCommitted('balance')?.value}, ` +
    `bonus=${engine.store.getCommitted('bonus')?.value}（Bob 的写入无一生效）`,
);

// ---------- 场景 2：自动重试 ----------
console.log('\n== 场景 2：冲突后自动重试 ==');
let attempts = 0;
await engine.run(async (txn) => {
  attempts += 1;
  const v = txn.get('balance') ?? 0;
  await sleep(1);
  txn.set('balance', v + 10);
});
console.log(`重试后提交成功: balance=${engine.store.getCommitted('balance')?.value}（本次无冲突，尝试 ${attempts} 次）`);

// ---------- 场景 3：高并发热点键 + 冲突率统计 ----------
console.log('\n== 场景 3：100 个并发事务争抢同一热点键 ==');
const hot = new OccEngine<string, number>({ maxRetries: 500 });
const workers = 100;
await Promise.all(
  Array.from({ length: workers }, () =>
    hot.run(async (txn) => {
      const v = txn.get('counter') ?? 0;
      await sleep(Math.random() * 2);
      txn.set('counter', v + 1);
    }),
  ),
);
const stats = hot.getStats();
console.log(`counter=${hot.store.getCommitted('counter')?.value}（期望 ${workers}，无丢失更新）`);
console.log(
  `尝试=${stats.attempts} 提交=${stats.commits} 中止=${stats.aborts} ` +
    `冲突率=${(stats.conflictRate * 100).toFixed(1)}%`,
);
