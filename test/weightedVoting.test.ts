import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WeightedVotingCRDT, type Tally } from '../src/weightedVoting.ts';

/** 生成规范化（键排序）的状态串，用于确定性等价比较。 */
function canonical(state: WeightedVotingCRDT): string {
  const snap = state.snapshot();
  const sortObj = (o: Record<string, unknown>) =>
    Object.fromEntries(Object.entries(o).sort(([a], [b]) => (a < b ? -1 : 1)));
  return JSON.stringify({
    votes: sortObj(snap.votes),
    weights: sortObj(snap.weights),
  });
}

function tallyOf(...states: WeightedVotingCRDT[]): Tally {
  const merged = new WeightedVotingCRDT('merger');
  for (const s of states) merged.merge(s);
  return merged.tally();
}

test('单节点投票与计票', () => {
  const a = new WeightedVotingCRDT('A');
  a.setWeight('A', 10);
  a.vote('YES');
  const t = a.tally();
  assert.equal(t.totalWeight, 10);
  assert.equal(t.yesWeight, 10);
  assert.equal(t.decision, 'APPROVED');
});

test('无权重配置的投票不计入总权重', () => {
  const a = new WeightedVotingCRDT('A');
  a.vote('YES');
  const t = a.tally();
  assert.equal(t.totalWeight, 0);
  assert.equal(t.yesWeight, 0);
  assert.equal(t.votedCount, 0);
  assert.equal(t.decision, 'PENDING');
});

test('重复投票以因果最新者为准（LWW）', () => {
  const a = new WeightedVotingCRDT('A');
  a.setWeight('A', 10);
  a.vote('YES');
  const stale = a.clone(); // 网络分区中对端只见到 YES
  a.vote('NO'); // 本节点改投 NO
  stale.merge(a);
  a.merge(stale);
  assert.equal(a.tally().noWeight, 10);
  assert.equal(stale.tally().noWeight, 10);
});

test('权重因果动态调整：愈合后旧配置不会覆盖新配置', () => {
  const a = new WeightedVotingCRDT('A');
  const b = new WeightedVotingCRDT('B');
  a.setWeight('X', 30);
  b.merge(a); // B 收到 30
  b.setWeight('X', 5); // B 因果更新为 5
  a.merge(b); // 愈合：A 收敛到 5
  assert.equal(a.tally().weights['X'], 5);
  const staleSnapshot = new WeightedVotingCRDT('S');
  staleSnapshot.setWeight('X', 30); // 迟到的旧配置（clock=1）
  a.merge(staleSnapshot); // 不应回滚
  assert.equal(a.tally().weights['X'], 5);
});

test('并发权重冲突按 (clock, nodeId) 确定性解决，与合并顺序无关', () => {
  const a = new WeightedVotingCRDT('A');
  const b = new WeightedVotingCRDT('B');
  a.setWeight('X', 100); // clock=1, nodeId=A
  b.setWeight('X', 200); // clock=1, nodeId=B（并发）
  const ab = a.clone().merge(b);
  const ba = b.clone().merge(a);
  assert.equal(ab.tally().weights['X'], 200); // nodeId 'B' 决胜
  assert.equal(ba.tally().weights['X'], 200);
  assert.equal(canonical(ab), canonical(ba));
});

test('合并满足交换律、结合律与幂等律', () => {
  const mk = (id: string, w: number, v: 'YES' | 'NO' | 'ABSTAIN') => {
    const n = new WeightedVotingCRDT(id);
    n.setWeight(id, w);
    n.vote(v);
    return n;
  };
  const [a, b, c] = [mk('A', 10, 'YES'), mk('B', 20, 'NO'), mk('C', 30, 'ABSTAIN')];

  const ab_c = a.clone().merge(b).merge(c);
  const ba_c = b.clone().merge(a).merge(c);
  const a_bc = a.clone().merge(b.clone().merge(c));
  assert.equal(canonical(ab_c), canonical(ba_c)); // 交换
  assert.equal(canonical(ab_c), canonical(a_bc)); // 结合

  const idem = ab_c.clone().merge(ab_c).merge(a);
  assert.equal(canonical(idem), canonical(ab_c)); // 幂等
});

test('决策边界：恰好半数不算通过，剩余票决定 PENDING/REJECTED', () => {
  const a = new WeightedVotingCRDT('A');
  a.setWeight('A', 50);
  a.setWeight('B', 50);
  a.vote('YES'); // yes=50, total=100，恰好一半 -> 未过半
  assert.equal(a.tally().decision, 'PENDING'); // B 尚未投票，仍可能过半

  const b = a.clone();
  b.setWeight('B', 0); // B 权重被移除：yes=50, total=50 -> 过半
  assert.equal(b.tally().decision, 'APPROVED');

  const c = new WeightedVotingCRDT('C');
  c.setWeight('C', 60);
  c.setWeight('B', 40);
  c.vote('NO'); // no=60，即便 B 全投赞成 yes=40 也无法过半 -> REJECTED
  assert.equal(c.tally().decision, 'REJECTED');
});

test('快照序列化往返保持状态一致', () => {
  const a = new WeightedVotingCRDT('A');
  a.setWeight('A', 10);
  a.setWeight('B', 20);
  a.vote('ABSTAIN');
  const restored = WeightedVotingCRDT.fromSnapshot(
    JSON.parse(JSON.stringify(a.snapshot())),
  );
  assert.equal(canonical(restored), canonical(a));
  assert.deepEqual(restored.tally(), a.tally());
});

test('验收：四节点网络分区下投票与动态调权，愈合后合并输出确定性决策', () => {
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
  assert.equal(A.tally().totalWeight, 100);

  // ---- 网络分区：{A, B} | {C, D} ----
  // 分区 1：A 赞成、B 反对；A 并发地将 C 的权重改为 35
  A.vote('YES');
  B.vote('NO');
  A.setWeight('C', 35);
  A.merge(B);
  B.merge(A);

  // 分区 2：C 赞成、D 弃权；D 并发地将 C 的权重改为 15；C 将 D 的权重提为 50
  C.vote('YES');
  D.vote('ABSTAIN');
  D.setWeight('C', 15);
  C.setWeight('D', 50);
  C.merge(D);
  D.merge(C);

  // ---- 网络愈合：任意顺序合并均应收敛到同一状态 ----
  const heal1 = A.clone().merge(B).merge(C).merge(D);
  const heal2 = D.clone().merge(C).merge(B).merge(A);
  const heal3 = A.clone().merge(C).merge(B.clone().merge(D));
  assert.equal(canonical(heal1), canonical(heal2));
  assert.equal(canonical(heal1), canonical(heal3));

  // ---- 验证最终计票 ----
  // 并发调权 C: (clock=3,A)->35 vs (clock=3,D)->15，nodeId 'D' 决胜 => C=15
  // 最终权重：A=10, B=20, C=15, D=50，全网有效总权重 = 95
  const t = heal1.tally();
  assert.equal(t.totalWeight, 95);
  assert.deepEqual(t.weights, { A: 10, B: 20, C: 15, D: 50 });
  assert.equal(t.yesWeight, 25); // A(10) + C(15)
  assert.equal(t.noWeight, 20); // B(20)
  assert.equal(t.abstainWeight, 50); // D(50) 弃权
  assert.equal(t.abstainCount, 1);
  assert.equal(t.pendingWeight, 0);
  assert.equal(t.votedCount, 4);
  // yes=25 未过半（95/2=47.5），且无剩余票 -> 确定性否决
  assert.equal(t.decision, 'REJECTED');

  // 任意合并顺序下计票结果完全一致（确定性）
  assert.deepEqual(tallyOf(A, B, C, D), tallyOf(D, C, B, A));
  assert.deepEqual(tallyOf(A, B, C, D), t);
});
