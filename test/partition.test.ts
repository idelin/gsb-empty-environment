import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/paxos/cluster.ts';
import { formatValue } from '../src/paxos/types.ts';

/**
 * 验收场景：5 节点集群网络分区。
 * 旧 Leader (n1) 与 n2 被隔离为少数派；多数派 (n3,n4,n5) 选出新 Leader。
 * 新 Leader 通过 Phase 1 补全未提交槽位，分区恢复后安全覆写少数派脏日志。
 */
test('网络分区：旧 Leader 被多数派新 Leader 取代，脏日志被安全覆写', () => {
  const cluster = new Cluster(5);
  const n1 = cluster.node('n1');
  const n3 = cluster.node('n3');

  // ---- 阶段 1：n1 当选并提交两条日志 ----
  n1.campaign();
  cluster.pump();
  n1.propose('v1');
  n1.propose('v2');
  cluster.pump();
  for (const log of cluster.committedLogs()) assert.deepEqual(log, ['v1', 'v2']);

  // ---- 阶段 2：网络分区 {n1,n2} | {n3,n4,n5} ----
  cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);

  // 旧 Leader 租约仍有效，在少数派继续提案：只能拿到 2 票，形成未提交的脏日志
  n1.propose('dirty-1');
  n1.propose('dirty-2');
  cluster.pump();
  assert.equal(n1.commitIndex, 2); // 少数派无法推进提交点
  assert.ok(cluster.network.droppedCount > 0); // 分区确实丢弃了消息
  assert.deepEqual(
    n1.logEntries.filter((e) => e.slot > 2).map((e) => e.value),
    ['dirty-1', 'dirty-2'],
  ); // 脏条目确实存在于少数派日志中

  // ---- 阶段 3：租约过期，多数派选出新 Leader ----
  cluster.advanceTime(cluster.leaseTtl + 1);
  n3.campaign();
  cluster.pump();
  assert.ok(n3.isLeader);
  assert.ok(n3.leaderBallot !== null && n1.leaderBallot !== null);
  assert.ok(
    n3.leaderBallot!.round > n1.leaderBallot!.round ||
      (n3.leaderBallot!.round === n1.leaderBallot!.round && n3.leaderBallot!.node > n1.leaderBallot!.node),
    'new leader must hold a higher ballot',
  );

  // 新 Leader 租约内绕过 Phase 1 连续提交
  const preparesBefore = cluster.network.stat('prepare');
  n3.propose('v3');
  n3.propose('v4');
  cluster.pump();
  assert.equal(cluster.network.stat('prepare'), preparesBefore);

  // 多数派已提交 v3/v4；少数派毫不知情
  for (const id of ['n3', 'n4', 'n5']) {
    assert.deepEqual([...cluster.node(id).applied], ['v1', 'v2', 'v3', 'v4']);
  }
  assert.equal(n1.commitIndex, 2);
  assert.equal(cluster.node('n2').commitIndex, 2);

  // ---- 阶段 4：分区恢复，新 Leader 继续提交并收敛全网 ----
  cluster.heal();
  n3.propose('v5');
  cluster.pump();

  // 全部 5 个节点提交日志严格一致、无空洞
  for (const [id, node] of cluster.nodes) {
    assert.deepEqual([...node.applied], ['v1', 'v2', 'v3', 'v4', 'v5'], `node ${id} diverged`);
    assert.equal(node.commitIndex, 5);
  }

  // 少数派脏值从未提交，且脏槽位已被安全覆写
  assert.deepEqual(
    n1.logEntries.map((e) => formatValue(e.value)),
    ['"v1"', '"v2"', '"v3"', '"v4"', '"v5"'],
  );
  assert.ok(![...n1.applied].includes('dirty-1'));
  assert.ok(![...n1.applied].includes('dirty-2'));

  // 旧 Leader 已下台，新 Leader 在位
  assert.equal(n1.isLeader, false);
  assert.ok(n3.isLeader);

  // ---- 严格无泄漏：全网无在途消息、无悬挂提案、无积压客户端请求 ----
  assert.equal(cluster.pendingMessages(), 0);
  for (const node of cluster.nodes.values()) {
    assert.equal(node.inflightCount, 0);
  }
});

test('多数派已接受的未提交值在 Leader 切换后必须保留（安全性）', () => {
  const cluster = new Cluster(5);
  const n1 = cluster.node('n1');
  const n4 = cluster.node('n4');

  n1.campaign();
  cluster.pump();
  n1.propose('v1');
  cluster.pump();

  // n1 发出 accept 后立刻被隔离：多数派接受了 'keep'，但 commit 未送达
  n1.propose('keep');
  cluster.partition(['n1'], ['n2', 'n3', 'n4', 'n5']);
  cluster.pump();
  assert.equal(n1.commitIndex, 1); // n1 收不到多数派 ack，无法提交

  // 多数派换主：新 Leader 必须通过 Phase 1 发现并重提 'keep'
  cluster.advanceTime(cluster.leaseTtl + 1);
  n4.campaign();
  cluster.pump();
  assert.ok(n4.isLeader);
  n4.propose('v3');
  cluster.pump();

  cluster.heal();
  n4.propose('v4');
  cluster.pump();

  // 'keep' 在槽位 2 被保留，所有节点一致
  for (const [id, node] of cluster.nodes) {
    assert.deepEqual([...node.applied], ['v1', 'keep', 'v3', 'v4'], `node ${id} lost a committed-safe value`);
  }
  assert.equal(cluster.pendingMessages(), 0);
  for (const node of cluster.nodes.values()) {
    assert.equal(node.inflightCount, 0);
  }
});
