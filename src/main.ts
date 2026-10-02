import { DeterministicKernel } from './kernel.ts';

/**
 * 运行样例：
 * 1. 录制含 50 个网络包到达 + 3 次超时的轨迹；
 * 2. 对每个历史事件拍摄快照（SHA-256）；
 * 3. 时间旅行到第 35 个事件，与历史快照逐字节比对；
 * 4. 全量校验 54 个快照（含初始状态）。
 */
const kernel = new DeterministicKernel();

console.log('=== 1. 录制轨迹 ===');
const { log, snapshots } = kernel.record({
  seed: 6,
  arrivalCount: 50,
  lossProb: 0.06,
  timeoutTicks: 8,
});

const arrivals = log.entries.filter((e) => e.kind === 'packet-arrival');
const timeouts = log.entries.filter((e) => e.kind === 'timeout');
console.log(`随机种子 seed=${log.header.seed} (envSeed=${log.header.envSeed})`);
console.log(`日志条目: ${log.entries.length} 条 = ${arrivals.length} 次包到达 + ${timeouts.length} 次超时`);
console.log('超时事件:', timeouts.map((e) => `#${e.index}@tick${e.tick}(seq=${e.payload.seq})`).join(', '));

console.log('\n=== 2. 历史快照（节选） ===');
for (const i of [0, 1, 2, 35, 52, 53]) {
  const s = snapshots[i];
  console.log(`事件 #${String(s.afterEvent).padStart(2)} tick=${String(s.tick).padStart(3)}  hash=${s.hash}`);
}

console.log('\n=== 3. 时间旅行到第 35 个事件 ===');
const target = 35;
const { snapshot, world } = kernel.travelTo(log, target);
const historical = snapshots[target];
console.log(`重放后状态 hash : ${snapshot.hash}`);
console.log(`历史快照 hash   : ${historical.hash}`);
console.log(`哈希一致        : ${snapshot.hash === historical.hash}`);
console.log(`字节逐一致      : ${snapshot.canonical === historical.canonical}`);
console.log(`第 35 事件后世界: tick=${world.tick}, 节点状态摘要:`);
const node = world.node as {
  pending: unknown[];
  completed: number[];
  timeouts: number;
  retransmissions: number;
  outbox: unknown[];
};
console.log(
  `  待确认=${node.pending.length} 已完成=${node.completed.length} 超时=${node.timeouts} 重传=${node.retransmissions} 已发送=${node.outbox.length}`,
);

console.log('\n=== 4. 全量校验（0..53 共 54 个快照） ===');
const ok = kernel.verify(log, snapshots);
console.log(`全部快照逐字节复现: ${ok}`);
if (!ok) process.exit(1);
