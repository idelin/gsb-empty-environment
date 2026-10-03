/**
 * 运行样例：3 节点集群（A/B/C）通信数轮，
 * 节点 A 依据矩阵时钟判定 LSN=10 之前的所有事件已被全网接收处理，
 * 安全触发底层物理日志截断。
 */
import { Cluster } from './cluster.ts';
import { MatrixClock } from './matrix-clock.ts';

const cluster = new Cluster(3, ['A', 'B', 'C']);
const [a] = cluster.nodes;

function communicationRound(tag: string): void {
  for (const node of cluster.nodes) node.localEvent(`${tag}: local op`);
  for (const node of cluster.nodes) {
    for (const peer of cluster.nodes) {
      if (peer.id !== node.id) node.send(peer.id, `${tag} payload`);
    }
  }
  cluster.runRound();
}

function showStatus(round: number): void {
  const horizon = a.clock.horizon();
  const first = a.records()[0]?.lsn ?? '-';
  const last = a.records().at(-1)?.lsn ?? '-';
  console.log(`--- 第 ${round} 轮通信后, 节点 A 状态 ---`);
  console.log(`A 的矩阵时钟 M[i][j] = A 认知的「节点 i 认知的节点 j 时钟」:`);
  console.log(a.clock.toString());
  console.log(`GC 水位 horizon = min_i M[i][j] = [${horizon.join(', ')}]`);
  console.log(`A 的物理日志: LSN ${first} .. ${last}`
    + ` (已截断至 LSN ${a.truncatedUpTo}), 当前可安全回收至 LSN ${a.computeGcLsn()}`);
  console.log();
}

console.log('=== 矩阵时钟 + 分布式垃圾回收 演示 (3 节点: A, B, C) ===\n');

// 每轮通信中 A 追加 5 条日志（1 local + 2 send + 2 receive）。
// 前两轮产生 LSN 1..10 的历史事件；后续轮次传播矩阵认知，
// 直到 A 的 GC 水位逐分量覆盖 LSN 1..10 全部事件的时间戳。
const TARGET_LSN = 10;
let round = 0;
while (a.computeGcLsn() < TARGET_LSN && round < 10) {
  round += 1;
  communicationRound(`r${round}`);
  showStatus(round);
}

const gcLsn = a.computeGcLsn();
console.log(`节点 A 依据矩阵时钟判定: LSN <= ${gcLsn} 的所有事件`
  + `已被集群内每一个节点完整接收并处理`);
if (gcLsn >= TARGET_LSN) {
  const before = a.records().length;
  const truncated = a.truncateLog(TARGET_LSN);
  console.log(`>>> 安全触发底层物理日志截断: 丢弃 LSN <= ${truncated} 的`
    + ` ${before - a.records().length} 条历史消息日志`);
  console.log(`>>> 截断后 A 的物理日志自 LSN ${a.records()[0]?.lsn} 起,`
    + ` 共 ${a.records().length} 条`);
} else {
  console.log('>>> 水位尚未覆盖目标 LSN, 暂不截断（保证安全）');
}

// 安全性自检：剩余日志中首条记录必须未被旧水位覆盖（否则截断点计算有误）。
const horizon = a.clock.horizon();
const firstRemaining = a.records()[0];
if (firstRemaining) {
  const stillCovered = MatrixClock.coveredBy(firstRemaining.timestamp, horizon);
  console.log(`\n安全性自检: 剩余首条记录 LSN ${firstRemaining.lsn} 时间戳`
    + ` [${firstRemaining.timestamp.join(', ')}] `
    + (stillCovered ? '仍被水位覆盖（可在下次 GC 回收）' : '未被水位覆盖（必须保留）'));
}
