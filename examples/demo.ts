// 运行样例：10 节点裂脑 -> 分区独立演化 -> 重连 -> 首次 PingReq 触发合并。
// 运行：node examples/demo.ts   （需要 Node >= 23.6，原生支持 TypeScript）
import { Cluster } from '../src/index.ts';

function printTable(cluster: Cluster, title: string): void {
  console.log(`\n--- ${title} ---`);
  console.log('node | domain | alive | ring-next | clock | merge');
  for (const node of cluster.nodes) {
    const info = node.partitionInfo();
    console.log(
      `${String(node.id).padStart(4)} | ${info.partitionId.padEnd(6)} | ${String(node.aliveCount()).padStart(5)} | ${String(node.ringSuccessor() ?? '-').padStart(9)} | ${String(node.clockRead()).padStart(5)} | ${node.mergeState}`,
    );
  }
}

const cluster = new Cluster(10);

console.log('== Phase 1: 10 节点集群启动，稳定运行 10 ticks ==');
cluster.run(10);
printTable(cluster, '初始闭环（单一域）');

const groupA = [1, 2, 3, 4, 5];
const groupB = [6, 7, 8, 9, 10];
console.log('\n== Phase 2: 网络分区 [1-5] | [6-10]，各自独立演化 100 ticks ==');
cluster.partition(groupA, groupB);
cluster.run(100);
printTable(cluster, '裂脑后：两个封闭子网，时钟各自漂移');
console.log(`分区期间丢包数: ${cluster.network.dropped}`);

const clocksBeforeHeal = cluster.clocks();
const maxClockBeforeHeal = Math.max(...clocksBeforeHeal);

console.log('\n== Phase 3: 网络愈合，等待首个跨子网 PingReq 触发合并握手 ==');
cluster.heal();
const used = cluster.runUntilConverged(300);
console.log(`愈合后 ${used} ticks 内完成合并（无死锁，看门狗上限 300）`);
printTable(cluster, '合并完成：单一域、视图一致、时钟已校正');

console.log(`\n时钟校正: 愈合前最大时钟=${maxClockBeforeHeal}, 合并后最小时钟=${Math.min(...cluster.clocks())}`);

console.log('\n--- 关键协议事件 ---');
for (const e of cluster.events.filter((x) =>
  ['foreign-contact', 'propose', 'accept', 'reject', 'commit', 'commit-applied', 'final'].includes(x.event),
)) {
  console.log(`tick=${String(e.tick).padStart(3)} node=${String(e.nodeId).padStart(2)} ${e.event.padEnd(15)} ${e.detail}`);
}

console.log('\n--- 报文统计（按类型投递数） ---');
for (const [kind, count] of [...cluster.network.deliveredByKind.entries()].sort()) {
  console.log(`${kind.padEnd(14)} ${count}`);
}
console.log(`完成合并的协调器实例数: ${cluster.nodes.filter((n) => n.mergesCompleted > 0).length}`);
