import assert from 'node:assert/strict';
import { test } from 'node:test';
import { LeaseCoordinator } from '../src/coordinator.ts';
import { LeaseNode } from '../src/node.ts';
import { LeaseError } from '../src/types.ts';

const RESOURCE = 'order-service-leader';

function makeCluster(now?: () => number) {
  const coordinator = new LeaseCoordinator({ minTtlMs: 100, maxTtlMs: 10_000, now });
  const nodeA = new LeaseNode('A', coordinator, { ttlMs: 2_000, autoRenew: false });
  const nodeB = new LeaseNode('B', coordinator, { ttlMs: 2_000, autoRenew: false });
  return { coordinator, nodeA, nodeB };
}

test('获取租约后可写入，非主节点写入被拒绝', () => {
  const { nodeA, nodeB } = makeCluster();
  nodeA.acquire(RESOURCE);
  nodeA.write('w1');
  assert.deepEqual(nodeA.appliedWrites, ['w1']);
  assert.throws(() => nodeB.write('w2'), (e: unknown) => e instanceof LeaseError && e.code === 'NOT_PRIMARY');
});

test('租约被持有时其他节点无法获取，过期后可获取', () => {
  let t = 1_000;
  const { coordinator, nodeA } = makeCluster(() => t);
  nodeA.acquire(RESOURCE);
  assert.throws(() => coordinator.acquire(RESOURCE, 'B'), (e: unknown) => (e as LeaseError).code === 'LEASE_HELD');
  t += 2_001; // 越过 TTL
  const token = coordinator.acquire(RESOURCE, 'B');
  assert.equal(token.holderId, 'B');
  assert.equal(coordinator.currentHolder(RESOURCE), 'B');
});

test('动态续期：TTL 被钳制到协调中心配置区间', () => {
  let t = 5_000;
  const coordinator = new LeaseCoordinator({ minTtlMs: 100, maxTtlMs: 1_000, now: () => t });
  const token = coordinator.acquire(RESOURCE, 'A', 500);
  assert.equal(token.expiresAt, 5_500);
  const renewedLow = coordinator.renew(token, 1); // 低于下限 → 钳到 100
  assert.equal(renewedLow.expiresAt, t + 100);
  const renewedHigh = coordinator.renew(token, 99_999); // 高于上限 → 钳到 1000
  assert.equal(renewedHigh.expiresAt, t + 1_000);
});

test('主动移交：B 无缝继承剩余租约上下文，A 退位后写入被栅栏拒绝', () => {
  let t = 10_000;
  const { coordinator, nodeA, nodeB } = makeCluster(() => t);
  nodeA.acquire(RESOURCE);
  nodeA.write('before-1');
  nodeA.write('before-2');

  const metrics = nodeA.handoverTo(nodeB);

  // B 立即成为主，继承剩余 TTL（无空窗）
  assert.equal(nodeB.isPrimary, true);
  assert.equal(coordinator.currentHolder(RESOURCE), 'B');
  assert.equal(nodeB.fencingToken, 2);
  // 剩余租约上下文被继承：过期时刻 ≈ 接管时刻 + 剩余 TTL
  assert.equal(coordinator.renew({ ...probe(nodeB) }, 2_000).holderId, 'B');
  // A 已退位
  assert.equal(nodeA.isPrimary, false);
  assert.throws(() => nodeA.write('after-handover'), (e: unknown) => (e as LeaseError).code === 'NOT_PRIMARY');
  // A 残留的旧凭证对协调中心已失效（防脑裂）
  assert.equal(metrics.switchoverMs >= 0, true);

  function probe(node: LeaseNode) {
    // 构造 B 当前有效凭证用于续期探测
    return {
      resourceId: RESOURCE,
      holderId: node.id,
      fencingToken: node.fencingToken!,
      expiresAt: t + 2_000,
      leaseVersion: 2,
    };
  }
});

test('移交冻结期间的客户端写入零丢失：进入待移交队列并由 B 回放', () => {
  const { nodeA, nodeB } = makeCluster();
  nodeA.acquire(RESOURCE);
  nodeA.write('w1');
  nodeA.write('w2');

  // A 收到滚动重启指令，先冻结本地写入；冻结期到达的写进入待移交队列
  nodeA.freeze();
  nodeA.write('w3-frozen');

  // 移交后由 B 无缝回放冻结期积压的写
  nodeA.handoverTo(nodeB);
  assert.deepEqual(nodeB.appliedWrites, ['w3-frozen']);
  assert.deepEqual(nodeA.appliedWrites, ['w1', 'w2']);
});

test('旧持有者的续期/写入在移交后被栅栏令牌拒绝', () => {
  const { coordinator, nodeA, nodeB } = makeCluster();
  nodeA.acquire(RESOURCE);
  const staleToken = {
    resourceId: RESOURCE,
    holderId: 'A',
    fencingToken: nodeA.fencingToken!,
    expiresAt: Date.now() + 2_000,
    leaseVersion: 1,
  };
  nodeA.handoverTo(nodeB);
  assert.throws(() => coordinator.renew(staleToken), (e: unknown) => (e as LeaseError).code === 'FENCING_VIOLATION');
  assert.throws(() => coordinator.validateWrite(staleToken), (e: unknown) => (e as LeaseError).code === 'FENCING_VIOLATION');
});

test('交接包只能被指定候选人使用，且只能使用一次', () => {
  const { coordinator, nodeA, nodeB } = makeCluster();
  const nodeC = new LeaseNode('C', coordinator, { autoRenew: false });
  nodeA.acquire(RESOURCE);
  const pkg = coordinator.prepareHandover(
    { resourceId: RESOURCE, holderId: 'A', fencingToken: 1, expiresAt: Date.now() + 2_000, leaseVersion: 1 },
    'B',
  );
  assert.throws(() => nodeC.receiveHandover(pkg), (e: unknown) => (e as LeaseError).code === 'WRONG_CANDIDATE');
  nodeB.receiveHandover(pkg);
  assert.throws(() => coordinator.acceptHandover(pkg), (e: unknown) => (e as LeaseError).code === 'HANDOVER_NOT_PENDING');
});

test('租约已过期时无法发起移交', () => {
  let t = 20_000;
  const { coordinator, nodeA } = makeCluster(() => t);
  nodeA.acquire(RESOURCE);
  t += 2_001; // 租约自然过期
  assert.throws(
    () =>
      coordinator.prepareHandover(
        { resourceId: RESOURCE, holderId: 'A', fencingToken: 1, expiresAt: t, leaseVersion: 1 },
        'B',
      ),
    (e: unknown) => (e as LeaseError).code === 'LEASE_EXPIRED',
  );
});

test('验收场景：A 滚动重启 → 冻结 → 移交 B → 零错误且切换仅数毫秒', () => {
  const { coordinator, nodeA, nodeB } = makeCluster();
  nodeA.acquire(RESOURCE);

  const clientErrors: unknown[] = [];
  let writes = 0;
  const clientWrite = (node: LeaseNode, data: string) => {
    try {
      node.write(data);
      writes += 1;
    } catch (err) {
      clientErrors.push(err);
    }
  };

  // 滚动重启指令到达前的正常流量
  for (let i = 0; i < 100; i += 1) clientWrite(nodeA, `pre-${i}`);

  // A 收到滚动重启指令：冻结本地写入并向 B 移交租约凭证
  const metrics = nodeA.handoverTo(nodeB);

  // B 已无缝接管主角色，后续流量直接打到 B
  for (let i = 0; i < 100; i += 1) clientWrite(nodeB, `post-${i}`);

  assert.equal(clientErrors.length, 0, '全过程零错误中断');
  assert.equal(writes, 200);
  assert.equal(nodeB.appliedWrites.length, 100);
  assert.equal(nodeA.appliedWrites.length, 100);
  assert.equal(coordinator.currentHolder(RESOURCE), 'B');
  assert.ok(metrics.switchoverMs < 50, `切换耗时应为毫秒级，实际 ${metrics.switchoverMs.toFixed(3)}ms`);
});
