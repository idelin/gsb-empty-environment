/**
 * 验收样例：误执行 UPDATE 清空 1000 条用户信息 -> 基于 CDC Before Image
 * 自动生成 1000 条反向补偿 SQL -> 回放恢复到误操作前的毫秒级原貌。
 */
import { InMemoryTable } from '../src/store.ts';
import { buildRollbackPackage, writeRollbackPackage } from '../src/rollback-package.ts';
import { replayStatements } from '../src/replay.ts';
import type { CdcEvent } from '../src/types.ts';
import assert from 'node:assert';

const USER_COUNT = 1000;

// 1. 建表并写入 1000 条用户数据（CDC 捕获开启）
const users = new InMemoryTable('users', 'id');
const cdcLog: CdcEvent[] = [];
users.onCdc((event) => cdcLog.push(event));

const seedTx = users.beginTx();
for (let i = 1; i <= USER_COUNT; i++) {
  users.insert(
    {
      id: i,
      name: `user_${i}`,
      email: `user_${i}@example.com`,
      balance: 1000 + i,
      updated_at: `2026-10-01T08:00:${String(i % 60).padStart(2, '0')}.000Z`,
    },
    seedTx,
  );
}
console.log(`[1] 初始化 ${users.size} 条用户数据，CDC 事件 ${cdcLog.length} 条`);

// 2. 误操作前的毫秒级快照（作为恢复基准）
const baseline = users.snapshot();

// 3. 人为误操作：一条 UPDATE 清空全部用户的 name/email/balance
const mistakeTx = users.beginTx();
const wiped = users.update(
  () => true,
  { name: null, email: '', balance: 0 },
  mistakeTx,
);
console.log(`[2] 误操作事务 ${mistakeTx}：UPDATE 影响 ${wiped} 行，name/email/balance 被清空`);

// 4. 灾备引擎读取 CDC 日志中的 Before Image，生成回滚包
const t0 = performance.now();
const pkg = buildRollbackPackage(cdcLog, { txId: mistakeTx });
const genMs = performance.now() - t0;
console.log(
  `[3] 回滚包 ${pkg.manifest.packageId}：${pkg.manifest.statementCount} 条补偿 DML，` +
    `生成耗时 ${genMs.toFixed(1)}ms，sha256=${pkg.manifest.sha256.slice(0, 12)}...`,
);
console.log('    首条补偿 SQL: ' + pkg.sql.split('\n')[1]);

// 5. 落盘回滚包（manifest.json / statements.json / rollback.sql）
const outDir = new URL('../artifacts/rollback-users/', import.meta.url).pathname;
writeRollbackPackage(pkg, outDir);
console.log(`[4] 回滚包已写入 ${outDir}`);

// 6. 回放补偿序列，执行快速冲正
const t1 = performance.now();
const { applied } = replayStatements(new Map([['users', users]]), pkg.statements);
const replayMs = performance.now() - t1;
console.log(`[5] 回放完成：应用 ${applied} 条补偿 DML，耗时 ${replayMs.toFixed(1)}ms`);

// 7. 校验：数据恢复到误操作前的毫秒级原貌
assert.deepStrictEqual(users.snapshot(), baseline, '恢复后的数据必须与误操作前完全一致');
console.log(`[6] 校验通过：${USER_COUNT} 条用户数据已恢复到误操作前的毫秒级原貌 ✅`);
