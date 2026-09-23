// 端到端演示：原始执行 → 崩溃恢复 → 逐步/回退/取消 → 分支拼接 → 检查点回收 → 损坏定位。
import { LogWriter, forkLog, spliceBranch, verifyChain } from '../src/log.ts';
import { Interpreter } from '../src/interpreter.ts';
import { createCheckpoint, recover, compact, reclaimCheckpoints } from '../src/checkpoint.ts';
import { fullLogInterpret } from '../src/differential.ts';
import { stateSummary, deepClone } from '../src/hash.ts';
import { LogCorruptionError, SpliceError } from '../src/types.ts';

const line = (s: string): void => console.log(s);
const head = (s: string): void => line(`\n=== ${s} ===`);

// ---------- 1. 原始执行：操作日志 + 事务 + 副作用确认 ----------
head('1. 原始执行（写日志，外部动作恰好执行一次）');
const w = new LogWriter();
const externalOutbox: string[] = [];
const doExternal = (eid: string): void => {
  externalOutbox.push(eid);
  line(`  [外部系统] 执行动作 ${eid}`);
};

w.append('op-1', { kind: 'kv.set', key: 'balance:ada', value: 100 });
w.append('op-2', { kind: 'kv.set', key: 'balance:bob', value: 50 });
line('  写入 op-1..op-2（开户）');

w.beginTx('tx-1');
w.append('op-3', { kind: 'kv.add', key: 'balance:ada', delta: -30 });
w.append('op-4', { kind: 'kv.add', key: 'balance:bob', delta: 30 });
line(`  事务 tx-1 暂存中，读者看到的 balance:ada = ${JSON.stringify(w.getState()['balance:ada'])}（中间态不可见）`);
w.commitTx('tx-1');
line(`  tx-1 提交后，读者看到的 balance:ada = ${JSON.stringify(w.getState()['balance:ada'])}`);

w.append('op-5', { kind: 'kv.set', key: 'profile:ada', value: { tier: 'gold' } }, 'eff-email-1');
doExternal('eff-email-1');
w.confirmEffect('eff-email-1', 'smtp 250 OK');
line('  eff-email-1 已确认（回执入日志）');

w.beginTx('tx-2');
w.append('op-6', { kind: 'kv.set', key: 'scratch:tmp', value: 1 });
w.abortTx('tx-2');
line('  tx-2 已取消（abort），其写入永不落库');

w.append('op-7', { kind: 'kv.add', key: 'points:ada', delta: 10 }, 'eff-points-1');
doExternal('eff-points-1');
line('  *** 崩溃！eff-points-1 的确认尚未写入日志 ***');

const log = w.records;
const originalSummary = stateSummary(w.getState());
line(`  原始状态摘要: ${originalSummary.slice(0, 16)}…  (日志 ${log.length} 条)`);

// ---------- 2. 崩溃恢复：只应用完整提交，不重放已确认副作用 ----------
head('2. 崩溃恢复（独立全日志解释器对照）');
const refired: string[] = [];
const { interpreter: rec1 } = recover(log, [], (eid) => {
  refired.push(eid);
  line(`  [恢复] 重新触发未确认的外部动作 ${eid}`);
});
const truth = fullLogInterpret(log);
line(`  恢复摘要 == 原始摘要: ${rec1.summary() === originalSummary}`);
line(`  恢复摘要 == 独立解释器: ${rec1.summary() === truth.summary}`);
line(`  重发的外部动作: ${JSON.stringify(refired)}（eff-email-1 已确认，不重发）`);

// ---------- 3. 逐步 / 回退 / 取消 ----------
head('3. 逐步、反向步进（逆补丁）、取消');
const itp = new Interpreter(log);
for (let i = 0; i < 5; i++) itp.step();
line(`  前进 5 步（tx-1 未提交）: balance:ada=${JSON.stringify(itp.getState()['balance:ada'])}（中间态不可见）`);
itp.step();
line(`  前进第 6 步（tx-1 提交）: balance:ada=${JSON.stringify(itp.getState()['balance:ada'])}`);
itp.stepBack();
line(`  回退 1 步（逆补丁撤销 tx-1 提交）: balance:ada=${JSON.stringify(itp.getState()['balance:ada'])}`);
itp.stepBack();
itp.stepBack();
line(`  再回退 2 步（撤销暂存的转账操作）: balance:ada=${JSON.stringify(itp.getState()['balance:ada'])}`);
itp.runToEnd();
line(`  重新前进到底: 摘要一致=${itp.summary() === originalSummary}`);
const itp2 = new Interpreter(log.slice(0, 3)); // 停在 tx-1 打开处
itp2.runToEnd();
line(`  打开的事务可以取消: cancel()=${itp2.cancel()}，状态不受影响 balance:ada=${JSON.stringify(itp2.getState()['balance:ada'])}`);

// ---------- 4. 分支与拼接 ----------
head('4. 分支与拼接（前缀摘要校验）');
const branch = forkLog(log, 'feature-vip', 2);
branch.append('b-op-1', { kind: 'kv.set', key: 'feature:vip', value: true });
const merged = spliceBranch(log, branch.records);
line(`  拼接成功: ${merged.length} 条，链校验 ok=${verifyChain(merged).ok}`);
const forged = deepClone(branch.records);
if (forged[0].body.kind === 'branch') forged[0].body.baseHash = 'aa'.repeat(32);
try {
  spliceBranch(log, forged);
} catch (e) {
  line(`  伪造基点摘要 → ${(e as SpliceError).message}`);
}

// ---------- 5. 检查点与回收 ----------
head('5. 检查点、日志压缩与回收');
const pre = new Interpreter(log.slice(0, 6));
pre.runToEnd();
const cp = createCheckpoint('cp@6', pre);
line(`  检查点 cp@6 绑定前缀摘要 ${cp.prefixHash.slice(0, 16)}…`);
const { segment, tail } = compact(log, cp);
line(`  压缩回收前 6 条历史，保留尾部 ${tail.length} 条（首条 prev 链接到检查点摘要）`);
const refired2: string[] = [];
const { interpreter: rec2, usedCheckpoint } = recover(tail, reclaimCheckpoints([segment.checkpoint]), (eid) =>
  refired2.push(eid),
);
line(`  从压缩段恢复: 使用检查点=${usedCheckpoint?.id}，摘要一致=${rec2.summary() === originalSummary}`);
line(`  增量恢复重发的动作: ${JSON.stringify(refired2)}（已确认的不重发）`);

// ---------- 6. 损坏定位 ----------
head('6. 日志损坏定位到操作序号（不可跳过冒充成功）');
const corrupted = deepClone(log);
const cbody = corrupted[3].body;
if (cbody.kind === 'op' && cbody.op.kind === 'kv.add') cbody.op.delta = -3000;
try {
  recover(corrupted, []);
} catch (e) {
  line(`  ${(e as LogCorruptionError).message}`);
}
const missing = log.filter((_, i) => i !== 4);
try {
  recover(missing, []);
} catch (e) {
  line(`  ${(e as LogCorruptionError).message}`);
}

head('完成');
line(`  最终校验: 恢复后最终摘要等于原状态 → ${rec1.summary() === originalSummary && rec2.summary() === originalSummary}`);
