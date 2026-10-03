/**
 * 运行样例：四个节点在网络分区下投票并动态调权，愈合后合并出确定性决策。
 * 运行：node examples/demo.ts   （需 Node >= 22.6，推荐 23+）
 */
import { WeightedVotingCRDT, type Tally } from '../src/weightedVoting.ts';

function show(label: string, t: Tally): void {
  console.log(`\n== ${label} ==`);
  console.log(`  权重配置: ${JSON.stringify(t.weights)}`);
  console.log(`  选票:     ${JSON.stringify(t.votes)}`);
  console.log(
    `  总权重=${t.totalWeight} 赞成=${t.yesWeight} 反对=${t.noWeight} ` +
      `弃权=${t.abstainWeight}(${t.abstainCount}票) 未投=${t.pendingWeight}`,
  );
  console.log(`  决策 => ${t.decision}`);
}

// ---- 初始：全网同步权重配置 A=10, B=20, C=30, D=40 ----
const A = new WeightedVotingCRDT('A');
const B = new WeightedVotingCRDT('B');
const C = new WeightedVotingCRDT('C');
const D = new WeightedVotingCRDT('D');
A.setWeight('A', 10);
B.setWeight('B', 20);
C.setWeight('C', 30);
D.setWeight('D', 40);
for (const n of [A, B, C, D]) n.merge(A).merge(B).merge(C).merge(D);
show('初始全网权重配置（总权重 100）', A.tally());

// ---- 网络分区：{A, B} | {C, D} ----
console.log('\n--- 网络分区：{A, B} | {C, D} ---');
A.vote('YES');
B.vote('NO');
A.setWeight('C', 35); // 分区1 中 A 提议给 C 调权 30 -> 35
A.merge(B);
B.merge(A);
show('分区1 {A,B} 局部视图', A.tally());

C.vote('YES');
D.vote('ABSTAIN');
D.setWeight('C', 15); // 分区2 中 D 并发提议给 C 调权 30 -> 15
C.setWeight('D', 50); // 分区2 中 C 提议给 D 调权 40 -> 50
C.merge(D);
D.merge(C);
show('分区2 {C,D} 局部视图', C.tally());

// ---- 网络愈合： gossip 合并（任意顺序均收敛） ----
console.log('\n--- 网络愈合：合并所有分区状态 ---');
const healed = A.clone().merge(C).merge(D).merge(B);
show('愈合后全网最终计票', healed.tally());

// 确定性验证：不同合并顺序结果一致
const alt = D.clone().merge(B).merge(A).merge(C);
console.log(
  `\n不同合并顺序决策一致: ${alt.tally().decision === healed.tally().decision}`,
);
