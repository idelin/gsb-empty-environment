import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WeightedVoting, type Tally } from '../src/index.ts';

/** 构造创世权重已全网同步的四节点集群。 */
function createCluster(): Record<'A' | 'B' | 'C' | 'D', WeightedVoting> {
  const A = new WeightedVoting('A');
  const B = new WeightedVoting('B');
  const C = new WeightedVoting('C');
  const D = new WeightedVoting('D');
  A.setWeight('A', 40);
  A.setWeight('B', 30);
  A.setWeight('C', 20);
  A.setWeight('D', 10);
  for (const node of [B, C, D]) node.merge(A);
  return { A, B, C, D };
}

test('单节点投票与 LWW 改票', () => {
  const node = new WeightedVoting('A');
  node.setWeight('A', 10);
  node.vote('yes');
  assert.equal(node.tally().yesWeight, 10);
  node.vote('no'); // 后投的票覆盖先投的票
  const tally = node.tally();
  assert.equal(tally.yesWeight, 0);
  assert.equal(tally.noWeight, 10);
});

test('权重因果生效：投票时刻之前的调权才影响该票', () => {
  const gov = new WeightedVoting('gov');
  const voter = new WeightedVoting('V');
  gov.setWeight('V', 10);
  voter.merge(gov);
  voter.vote('yes'); // 按权重 10 计
  gov.merge(voter); // 投票结果先因果传播到治理节点
  gov.setWeight('V', 99); // 投票之后才发生的调权
  voter.merge(gov);
  const tally = voter.tally();
  assert.equal(tally.yesWeight, 10, '旧票仍按投票时刻的权重计');
  assert.equal(tally.totalWeight, 99, '总权重按最新配置计');
  voter.vote('yes'); // 改票，按新权重计
  assert.equal(voter.tally().yesWeight, 99);
});

test('合并满足幂等、可交换（任意顺序收敛同一结果）', () => {
  const { A, B, C, D } = createCluster();
  A.vote('yes');
  B.vote('no');
  C.vote('abstain');
  D.vote('yes');

  const mergeAll = (order: WeightedVoting[]): Tally => {
    const auditor = new WeightedVoting('auditor');
    for (const replica of order) auditor.merge(replica);
    return auditor.tally();
  };
  const forward = mergeAll([A, B, C, D]);
  const reverse = mergeAll([D, C, B, A]);
  const shuffled = mergeAll([C, A, D, B]);
  assert.deepEqual(forward, reverse);
  assert.deepEqual(forward, shuffled);

  const before = JSON.stringify(A.tally());
  A.merge(B).merge(B).merge(A); // 幂等：重复合并不改变状态
  assert.equal(JSON.stringify(A.tally()), JSON.stringify(A.merge(B).tally()));
  assert.notEqual(before, undefined);
});

test('同一选民在两个分区并发改票，愈合后确定性收敛', () => {
  const left = new WeightedVoting('shared');
  left.setWeight('shared', 5);
  const right = WeightedVoting.fromJSON(left.toJSON());
  left.vote('yes');
  right.vote('yes');
  right.vote('no'); // right 的时钟更靠后
  left.merge(right);
  const mergedRight = WeightedVoting.fromJSON(right.toJSON());
  mergedRight.merge(left);
  assert.equal(left.tally().noWeight, 5);
  assert.deepEqual(left.tally(), mergedRight.tally(), '两个方向合并结果一致');
});

test('序列化往返保持状态与决策不变', () => {
  const { A, B } = createCluster();
  A.vote('yes');
  B.vote('abstain');
  A.merge(B);
  const restored = WeightedVoting.fromJSON(JSON.parse(JSON.stringify(A.toJSON())));
  assert.deepEqual(restored.tally(), A.tally());
});

test('决策规则：approved / rejected / pending', () => {
  const cluster = createCluster();
  cluster.A.vote('yes');
  cluster.B.vote('yes');
  cluster.C.vote('no');
  cluster.D.vote('no');
  const healed = new WeightedVoting('auditor');
  for (const replica of Object.values(cluster)) healed.merge(replica);
  assert.equal(healed.tally().decision, 'approved'); // 70 > 100/2

  const rejected = createCluster();
  rejected.A.vote('no');
  rejected.B.vote('no');
  const healedNo = new WeightedVoting('auditor');
  for (const replica of Object.values(rejected)) healedNo.merge(replica);
  const noTally = healedNo.tally();
  assert.equal(noTally.decision, 'rejected'); // 70 >= 100/2
  assert.equal(noTally.quorum, true);

  const pending = createCluster();
  pending.D.vote('yes'); // 仅 10/100 参与，未达法定人数
  const healedPending = new WeightedVoting('auditor');
  for (const replica of Object.values(pending)) healedPending.merge(replica);
  const pendingTally = healedPending.tally();
  assert.equal(pendingTally.decision, 'pending');
  assert.equal(pendingTally.quorum, false);
});

test('验收场景：四节点分区投票 + 动态调权，愈合后输出确定性决策', () => {
  const { A, B, C, D } = createCluster();

  // 网络分区：{A, B} | {C, D}
  // P1：先把 B 的权重 30 -> 50，再投票
  A.setWeight('B', 50);
  B.merge(A);
  A.vote('yes');
  B.vote('yes');
  A.merge(B);
  B.merge(A);

  // P2：D 先投弃权，之后 D 的权重才 10 -> 25（因果上不影响该票）
  C.vote('no');
  D.vote('abstain');
  C.setWeight('D', 25);
  D.merge(C);
  C.merge(D);

  // 网络愈合：反熵同步直至收敛
  A.merge(C);
  B.merge(D);
  C.merge(A);
  D.merge(B);

  const expected: Tally = {
    yesWeight: 90, // A(40) + B(50，调权后投的票)
    noWeight: 20, // C(20)
    abstainWeight: 10, // D 弃权票早于调权，仍按 10 计
    votedWeight: 120,
    totalWeight: 135, // 40 + 50 + 20 + 25
    quorum: true,
    decision: 'approved', // 90 * 2 > 135
  };
  for (const [id, replica] of Object.entries({ A, B, C, D })) {
    assert.deepEqual(replica.tally(), expected, `节点 ${id} 收敛到同一决策`);
  }

  // 任意合并顺序得到相同结果（确定性）
  const orders = [
    ['A', 'B', 'C', 'D'],
    ['D', 'C', 'B', 'A'],
    ['B', 'D', 'A', 'C'],
  ] as const;
  const replicas = { A, B, C, D };
  for (const order of orders) {
    const auditor = new WeightedVoting(`auditor-${order.join('')}`);
    for (const id of order) auditor.merge(replicas[id]);
    assert.deepEqual(auditor.tally(), expected, `合并顺序 ${order.join('->')}`);
  }
});
