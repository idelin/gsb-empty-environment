import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster, compareDomains, mergeViews, domainOf } from '../src/index.ts';
import type { MemberState } from '../src/index.ts';

const GROUP_A = [1, 2, 3, 4, 5];
const GROUP_B = [6, 7, 8, 9, 10];

/** 构造：稳定 10 ticks -> 分区 -> 独立演化 100 ticks。 */
function partitionedCluster(): Cluster {
  const cluster = new Cluster(10);
  cluster.run(10);
  cluster.partition(GROUP_A, GROUP_B);
  cluster.run(100);
  return cluster;
}

test('分区后形成两个封闭子网环，域标识分裂', () => {
  const cluster = partitionedCluster();

  for (const id of GROUP_A) {
    assert.equal(cluster.node(id).partitionInfo().partitionId, 'P1');
    assert.equal(cluster.node(id).aliveCount(), 5);
  }
  for (const id of GROUP_B) {
    assert.equal(cluster.node(id).partitionInfo().partitionId, 'P6');
    assert.equal(cluster.node(id).aliveCount(), 5);
  }
  // 闭环：5 的后继回绕到 1，10 的后继回绕到 6
  assert.equal(cluster.node(5).ringSuccessor(), 1);
  assert.equal(cluster.node(1).ringSuccessor(), 2);
  assert.equal(cluster.node(10).ringSuccessor(), 6);
  assert.equal(cluster.node(6).ringSuccessor(), 7);
  // 分区期间确实有跨子网报文被丢弃
  assert.ok(cluster.network.dropped > 0);
  // 两侧时钟独立漂移，不再同步
  assert.ok(new Set(cluster.partitionIds()).size === 2);
});

test('重连后首个跨子网 PingReq 成功触发合并握手并无死锁收敛', () => {
  const cluster = partitionedCluster();
  cluster.heal();
  const used = cluster.runUntilConverged(300); // 看门狗：超时未收敛即抛死锁错误
  assert.ok(used < 300, `应在有界 tick 内收敛，实际 ${used}`);

  // 首个异域接触必须发生在 PingReq 路径上（间接 Ping 或 ReqAck 回报）
  const firstContact = cluster.eventsNamed('foreign-contact')[0];
  assert.ok(firstContact, '必须发生异域接触');
  assert.match(firstContact.detail, /via=PING_REQ/);

  // 握手协议确实经历 propose -> accept -> commit -> final
  for (const phase of ['propose', 'accept', 'commit', 'commit-applied', 'final']) {
    assert.ok(cluster.eventsNamed(phase).length > 0, `缺少协议阶段 ${phase}`);
  }
});

test('同时提案由确定性优先级裁决（出现 reject 且不死锁）', () => {
  const cluster = partitionedCluster();
  cluster.heal();
  cluster.runUntilConverged(300);

  // 两侧网关几乎同时发现对方并提案 => 必然出现优先级拒绝
  assert.ok(cluster.eventsNamed('reject').length > 0, '应发生同时提案的拒绝裁决');
  // 最终所有节点握手状态复位
  for (const node of cluster.nodes) assert.equal(node.mergeState, 'idle');
});

test('合并后拓扑融合：全员 10 节点存活、视图逐字节一致、单一域', () => {
  const cluster = partitionedCluster();
  cluster.heal();
  cluster.runUntilConverged(300);

  assert.ok(cluster.converged());
  assert.equal(new Set(cluster.partitionIds()).size, 1);
  assert.equal(cluster.partitionIds()[0], 'P1');
  assert.equal(new Set(cluster.viewStrings()).size, 1);
  for (const node of cluster.nodes) {
    assert.equal(node.aliveCount(), 10);
    const view = node.viewSnapshot();
    assert.ok(view.every((m) => m.status === 'alive'));
    // 闭环恢复为完整 10 节点环
    assert.equal(node.ringSuccessor(), (node.id % 10) + 1);
  }
});

test('时钟校正：合并后所有节点时钟越过分裂前最大值且保持单调', () => {
  const cluster = partitionedCluster();
  const clocksAtHeal = cluster.clocks();
  const maxBeforeHeal = Math.max(...clocksAtHeal);
  cluster.heal();
  cluster.runUntilConverged(300);

  const clocksAfter = cluster.clocks();
  for (let i = 0; i < 10; i++) {
    assert.ok(clocksAfter[i] > maxBeforeHeal, `节点 ${i + 1} 时钟应被校正到分裂前最大值之上`);
    assert.ok(clocksAfter[i] >= clocksAtHeal[i], `节点 ${i + 1} 时钟不得回退`);
  }
});

test('域优先级是全序：同时提案必有唯一胜者', () => {
  const a = domainOf([
    { id: 1, status: 'alive', incarnation: 0 },
    { id: 2, status: 'alive', incarnation: 0 },
  ]);
  const b = domainOf([
    { id: 3, status: 'alive', incarnation: 0 },
    { id: 4, status: 'alive', incarnation: 0 },
  ]);
  assert.ok(compareDomains(a, b) > 0);
  assert.ok(compareDomains(b, a) < 0);
  assert.notEqual(compareDomains(a, b), 0);
  // 存活数多者优先
  const bigger = domainOf([
    { id: 5, status: 'alive', incarnation: 0 },
    { id: 6, status: 'alive', incarnation: 0 },
    { id: 7, status: 'alive', incarnation: 0 },
  ]);
  assert.ok(compareDomains(bigger, a) > 0);
});

test('视图合并规则：incarnation 高者胜，并列时存活优先，自身永不判死', () => {
  const viewA: MemberState[] = [
    { id: 1, status: 'alive', incarnation: 2 },
    { id: 2, status: 'dead', incarnation: 1 },
  ];
  const viewB: MemberState[] = [
    { id: 1, status: 'dead', incarnation: 1 },
    { id: 2, status: 'alive', incarnation: 1 },
    { id: 3, status: 'alive', incarnation: 0 },
  ];
  const merged = mergeViews(viewA, viewB, 1);
  assert.deepEqual(merged, [
    { id: 1, status: 'alive', incarnation: 2 }, // incarnation 高者胜
    { id: 2, status: 'alive', incarnation: 1 }, // 并列时 alive 优先（愈合乐观规则）
    { id: 3, status: 'alive', incarnation: 0 }, // 并集
  ]);
  // 若合并结果判死了自己，则强制复活并提升 incarnation
  const selfDead = mergeViews(
    [{ id: 1, status: 'alive', incarnation: 0 }],
    [{ id: 1, status: 'dead', incarnation: 5 }],
    1,
  );
  assert.deepEqual(selfDead, [{ id: 1, status: 'alive', incarnation: 6 }]);
});
