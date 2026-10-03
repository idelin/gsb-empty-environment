import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/paxos/cluster.ts';
import { NOOP } from '../src/paxos/types.ts';

test('竞选产生唯一领导者，租约内提案绕过 Phase 1 直接提交', () => {
  const cluster = new Cluster(5);
  const n1 = cluster.node('n1');

  n1.campaign();
  cluster.pump();
  assert.ok(n1.isLeader);
  assert.ok(n1.leaseValid);

  // 稳态提案：不得产生任何新的 prepare 消息（绕过 Phase 1）
  const preparesBefore = cluster.network.stat('prepare');
  n1.propose('a');
  n1.propose('b');
  n1.propose('c');
  cluster.pump();
  assert.equal(cluster.network.stat('prepare'), preparesBefore);

  // 全部 5 个节点提交日志一致且无空洞
  for (const log of cluster.committedLogs()) {
    assert.deepEqual(log, ['a', 'b', 'c']);
  }
  for (const node of cluster.nodes.values()) {
    assert.equal(node.commitIndex, 3);
    assert.equal(node.inflightCount, 0);
  }
  assert.equal(cluster.pendingMessages(), 0);
});

test('租约过期后提案自动重新竞选（重新执行 Phase 1）', () => {
  const cluster = new Cluster(5);
  const n1 = cluster.node('n1');
  n1.campaign();
  cluster.pump();
  n1.propose('x');
  cluster.pump();

  const preparesBefore = cluster.network.stat('prepare');
  cluster.advanceTime(cluster.leaseTtl + 1); // 租约过期
  assert.equal(n1.leaseValid, false);

  n1.propose('y');
  cluster.pump();
  assert.ok(cluster.network.stat('prepare') > preparesBefore); // 重新走了 Phase 1
  for (const log of cluster.committedLogs()) {
    assert.deepEqual(log, ['x', 'y']);
  }
  assert.equal(cluster.pendingMessages(), 0);
});

test('多数派已接受但存在空洞：新领导者 Phase 1 用 NOOP 补洞后连续提交', () => {
  const cluster = new Cluster(5);
  // 手工构造：n3/n4/n5 接受了槽位 1 和 3（槽位 2 是空洞）
  for (const id of ['n3', 'n4', 'n5']) {
    const node = cluster.node(id);
    node.handle({ type: 'accept', from: 'n1', to: id, slot: 1, ballot: { round: 1, node: 'n1' }, value: 'x' });
    node.handle({ type: 'accept', from: 'n1', to: id, slot: 3, ballot: { round: 1, node: 'n1' }, value: 'z' });
  }
  cluster.pump(); // 排空 accepted 回复

  const n5 = cluster.node('n5');
  n5.campaign();
  cluster.pump();
  assert.ok(n5.isLeader);

  // 空洞槽位 2 必须以 NOOP 填补，提交前缀严格连续
  for (const log of cluster.committedLogs()) {
    assert.deepEqual(log, ['x', NOOP, 'z']);
  }
  for (const node of cluster.nodes.values()) {
    assert.equal(node.commitIndex, 3);
    assert.equal(node.inflightCount, 0);
  }
  assert.equal(cluster.pendingMessages(), 0);
});

test('低选票的 Prepare 被拒绝（promise-reject），现任领导者不受影响', () => {
  const cluster = new Cluster(5);
  cluster.network.register('n0'); // 外部伪造节点
  const n2 = cluster.node('n2');

  n2.campaign();
  cluster.pump();
  assert.ok(n2.isLeader);

  // 注入过期选票 (0, n0) 的 Prepare => 全部节点必须拒绝
  for (const id of cluster.nodes.keys()) {
    cluster.node(id).handle({
      type: 'prepare',
      from: 'n0',
      to: id,
      ballot: { round: 0, node: 'n0' },
      commitIndex: 0,
    });
  }
  cluster.pump();
  assert.equal(cluster.network.stat('promise-reject'), 5);
  assert.ok(n2.isLeader);

  // 排空伪造节点收件箱，保证无泄漏
  while (cluster.network.dequeue('n0') !== undefined) {
    // drain
  }
  assert.equal(cluster.pendingMessages(), 0);
});

test('分区中旧领导者的过期 Accept 被拒绝（accept-reject）并下台', () => {
  const cluster = new Cluster(3);
  const n1 = cluster.node('n1');
  const n2 = cluster.node('n2');

  n1.campaign();
  cluster.pump();
  assert.ok(n1.isLeader);

  // n1 被分区隔离，n2 以更高选票当选
  cluster.partition(['n1'], ['n2', 'n3']);
  n2.campaign();
  cluster.pump();
  assert.ok(n2.isLeader);
  cluster.heal();

  // n1 租约未过期（时间未推进），试图直接提交 => 选票已过期，被多数派拒绝
  assert.ok(n1.leaseValid);
  n1.propose('stale');
  cluster.pump();
  assert.ok(cluster.network.stat('accept-reject') >= 2);
  assert.equal(n1.isLeader, false);

  // 集群仍可用：n2 正常提交，n1 的脏条目被安全覆写
  n2.propose('fresh');
  cluster.pump();
  for (const log of cluster.committedLogs()) {
    assert.deepEqual(log, ['fresh']);
  }
  assert.equal(cluster.pendingMessages(), 0);
});
