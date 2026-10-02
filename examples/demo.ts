/**
 * 运行样例：多分区确定性事务状态机验收场景。
 *
 *  1. 跨分区事务 T1/T2 并发交错，注入乱序网络包 -> 最终状态一致；
 *  2. 未决冲突输出可审计决胜证据；
 *  3. 快照切片点之后重放日志 -> 状态完全一致（崩溃恢复）。
 */

import { StateMachine } from '../src/stateMachine.ts';
import { buildLog, shuffledDelivery, PARTITION_COUNT, SNAPSHOT_AT } from '../src/scenario.ts';

const OPTS = { partitionCount: PARTITION_COUNT };
const log = buildLog();

console.log('=== 1. 乱序网络包下的确定性 ===');
const inOrder = new StateMachine(OPTS);
for (const e of log) inOrder.ingest(e);

const shuffled = new StateMachine(OPTS);
const delivered = shuffledDelivery(log, 42);
console.log(`日志 ${log.length} 个事件，乱序投递 ${delivered.length} 个包（含 1 个重复包）`);
console.log('投递顺序(seq):', delivered.map((e) => e.seq).join(' '));
for (const e of delivered) shuffled.ingest(e);

console.log('顺序回放终态:', inOrder.canonicalState());
console.log('乱序回放终态:', shuffled.canonicalState());
console.log('终态一致:', inOrder.canonicalState() === shuffled.canonicalState());
console.log('提交顺序一致:', JSON.stringify(inOrder.committed) === JSON.stringify(shuffled.committed),
  inOrder.committed);

console.log('\n=== 2. 未决冲突的可审计决胜证据 ===');
for (const e of inOrder.evidence) console.log(JSON.stringify(e, null, 2));

console.log('=== 3. 快照切片点 + 崩溃恢复 ===');
const live = new StateMachine(OPTS);
for (const e of log.slice(0, SNAPSHOT_AT)) live.ingest(e);
const snapshot = live.createSnapshot();
console.log(`切片点 nextSeq=${snapshot.nextSeq}，截断后保留日志=${live.retainedLog.length} 条`);
console.log('快照内容:', JSON.stringify(snapshot));
for (const e of log.slice(SNAPSHOT_AT)) live.ingest(e);

// 模拟崩溃：丢弃内存态，仅从快照 + 切片点之后的乱序日志恢复。
const recovered = StateMachine.recover(snapshot, shuffledDelivery(log.slice(SNAPSHOT_AT), 7), OPTS);
console.log('在线实例终态:', live.canonicalState());
console.log('崩溃恢复终态:', recovered.canonicalState());
console.log('全量回放终态:', inOrder.canonicalState());
console.log(
  '三者一致:',
  live.canonicalState() === inOrder.canonicalState() &&
    recovered.canonicalState() === inOrder.canonicalState(),
);
