/**
 * 运行样例：跨 2 分区的并发事务 T1/T2，注入乱序网络包，
 * 验证最终状态一致；随后生成快照并重放后续日志验证崩溃恢复。
 *
 * 运行：node demo/run.ts   （Node >= 22.6，推荐 23.6+）
 */
import { DeterministicLog } from '../src/log.ts';
import { StateMachine } from '../src/stateMachine.ts';
import type { TxnEvent } from '../src/types.ts';

function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function shuffle<T>(items: T[], rand: () => number): T[] {
  const arr = [...items];
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [arr[i], arr[j]] = [arr[j]!, arr[i]!];
  }
  return arr;
}

const probe = new StateMachine({ numPartitions: 2 });
const keyOf = (p: number, prefix: string) => {
  for (let i = 0; ; i++) {
    const k = `${prefix}${i}`;
    if (probe.partitionOf(k) === p) return k;
  }
  throw new Error('unreachable');
};

const keyA = keyOf(0, 'acc-alice-'); // 分区 0
const keyB = keyOf(1, 'acc-bob-'); // 分区 1
const shared = keyOf(0, 'counter-'); // 双方竞争，分区 0

console.log('=== 多分区确定性事务状态机：运行样例 ===\n');
console.log(`分区映射: ${keyA} -> P${probe.partitionOf(keyA)}, ` +
  `${keyB} -> P${probe.partitionOf(keyB)}, ${shared} -> P${probe.partitionOf(shared)}`);

// T1 与 T2 均跨 2 个分区，且在 shared 键上写-写冲突。
const events: TxnEvent[] = [
  { kind: 'prepare', txnId: 'T1', lamport: 1, reads: [keyB],
    writes: { [keyA]: '100', [shared]: 'T1' } },
  { kind: 'commit', txnId: 'T1' },
  { kind: 'prepare', txnId: 'T2', lamport: 2, reads: [keyA],
    writes: { [keyB]: '200', [shared]: 'T2' } },
  { kind: 'commit', txnId: 'T2' },
];

console.log('\n--- 1. 乱序网络包注入（5 种投递顺序） ---');
const states: string[] = [];
for (let seed = 1; seed <= 5; seed++) {
  const delivered = shuffle(events, rng(seed));
  const sm = new StateMachine({ numPartitions: 2 });
  const log = new DeterministicLog();
  for (const e of delivered) sm.applyEntry(log.append(e));
  sm.seal();
  const state = JSON.stringify(sm.state());
  states.push(state);
  console.log(`seed=${seed} 投递顺序: ${delivered.map((e) => `${e.kind}(${e.txnId})`).join(' ')}`);
  console.log(`         最终状态: ${state}`);
}
console.log(`一致性: ${new Set(states).size === 1 ? 'OK —— 5 种乱序投递的最终状态完全相同' : 'FAIL'}`);

console.log('\n--- 2. 未决冲突的可审计决胜证据 ---');
{
  const sm = new StateMachine({ numPartitions: 2 });
  const log = new DeterministicLog();
  for (const e of events) sm.applyEntry(log.append(e));
  sm.seal();
  for (const ev of sm.audit.ofType('tie-break')) {
    console.log(JSON.stringify(ev, null, 2));
  }
  console.log(`应用顺序(拓扑排序): ${sm.getAppliedOrder().join(' -> ')}`);
}

console.log('\n--- 3. 快照切片点 + 崩溃恢复回放 ---');
{
  const more: TxnEvent[] = [
    { kind: 'prepare', txnId: 'T3', lamport: 3, reads: [], writes: { [keyA]: '150' } },
    { kind: 'commit', txnId: 'T3' },
    { kind: 'prepare', txnId: 'T4', lamport: 4, reads: [shared], writes: { [keyB]: '250' } },
    { kind: 'commit', txnId: 'T4' },
  ];
  const all = [...events, ...more];

  // 基准：一次性完整回放。
  const full = new StateMachine({ numPartitions: 2 });
  const fullLog = new DeterministicLog();
  for (const e of all) full.applyEntry(fullLog.append(e));
  full.seal();

  // 模拟：先消费前 6 条日志 -> 生成快照（崩溃）-> 恢复快照 -> 重放剩余日志。
  const cut = 6;
  const before = new StateMachine({ numPartitions: 2 });
  const log = new DeterministicLog();
  for (const e of all.slice(0, cut)) before.applyEntry(log.append(e));
  before.seal();
  const snapshotFile = JSON.stringify(before.snapshot());
  console.log(`切片点 seq=${before.snapshot().consumedSeq}，快照大小 ${snapshotFile.length} 字节`);
  console.log('（模拟崩溃）');

  const restored = StateMachine.restore(JSON.parse(snapshotFile));
  for (const entry of log.replayFrom(restored.consumedSeq)) restored.applyEntry(entry);
  for (const e of all.slice(cut)) restored.applyEntry(log.append(e));
  restored.seal();

  const a = JSON.stringify(restored.state());
  const b = JSON.stringify(full.state());
  console.log(`恢复后状态: ${a}`);
  console.log(`基准状态:   ${b}`);
  console.log(`一致性: ${a === b ? 'OK —— 快照 + 切片点回放与完整回放完全一致' : 'FAIL'}`);
}
