/**
 * 运行样例：四个节点在网络分区下投票 + 动态调权，愈合后合并出确定性决策。
 *
 * 时间线：
 *   1. 全网同步创世权重：A=40, B=30, C=20, D=10（总权重 100）
 *   2. 网络分区：{A, B} | {C, D}
 *   3. 分区 P1：A 把 B 的权重提升为 50，随后 A 投赞成、B 投赞成（B 按新权重 50 计）
 *   4. 分区 P2：C 投反对；D 先投弃权，之后 C 才把 D 的权重提升为 25
 *      —— D 的弃权票在因果上早于调权，仍按旧权重 10 计
 *   5. 网络愈合：四个副本两两合并，收敛后输出全网决策
 */
import { WeightedVoting, type Tally } from '../src/index.ts';

function printTally(label: string, tally: Tally): void {
  console.log(`\n[${label}]`);
  console.log(`  赞成权重   yesWeight     = ${tally.yesWeight}`);
  console.log(`  反对权重   noWeight      = ${tally.noWeight}`);
  console.log(`  弃权权重   abstainWeight = ${tally.abstainWeight}`);
  console.log(`  已投票权重 votedWeight   = ${tally.votedWeight}`);
  console.log(`  全网总权重 totalWeight   = ${tally.totalWeight}`);
  console.log(`  法定人数   quorum        = ${tally.quorum}`);
  console.log(`  最终决策   decision      = ${tally.decision}`);
}

// ---- 1. 创世：四个节点，A 作为治理节点配置初始权重并全网广播 ----
const A = new WeightedVoting('A');
const B = new WeightedVoting('B');
const C = new WeightedVoting('C');
const D = new WeightedVoting('D');

A.setWeight('A', 40);
A.setWeight('B', 30);
A.setWeight('C', 20);
A.setWeight('D', 10);
for (const node of [B, C, D]) node.merge(A); // 分区前最后一次全网同步

// ---- 2. 网络分区：{A, B} 与 {C, D} 互不通信 ----

// 分区 P1：先调权再投票 —— B 的赞成票按新权重 50 计
A.setWeight('B', 50);
B.merge(A);
A.vote('yes');
B.vote('yes');
A.merge(B);
B.merge(A);

// 分区 P2：先投票再调权 —— D 的弃权票因果上早于调权，仍按旧权重 10 计
C.vote('no');
D.vote('abstain');
C.setWeight('D', 25);
D.merge(C);
C.merge(D);

console.log('=== 分区期间各自视角 ===');
printTally('P1 视角（A/B）', A.tally());
printTally('P2 视角（C/D）', C.tally());

// ---- 3. 网络愈合：两两合并直至收敛（反熵同步） ----
A.merge(C);
B.merge(D);
C.merge(A);
D.merge(B);
A.merge(D); // 幂等，重复合并不改变结果

console.log('\n=== 愈合后全网共识 ===');
printTally('合并后全网视角', A.tally());

// ---- 4. 确定性验证：任意合并顺序收敛到同一结果 ----
const orders: string[][] = [
  ['A', 'B', 'C', 'D'],
  ['D', 'C', 'B', 'A'],
  ['B', 'D', 'A', 'C'],
];
const replicas: Record<string, WeightedVoting> = { A, B, C, D };
const fingerprints = orders.map((order) => {
  const merged = new WeightedVoting(`auditor-${order.join('')}`);
  for (const id of order) merged.merge(replicas[id]);
  return JSON.stringify(merged.tally());
});
const deterministic = fingerprints.every((f) => f === fingerprints[0]);
console.log(`\n合并顺序 ${orders.map((o) => o.join('→')).join(' / ')}`);
console.log(`结果一致（确定性收敛）: ${deterministic}`);
if (!deterministic) process.exitCode = 1;
