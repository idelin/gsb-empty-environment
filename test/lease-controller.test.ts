import { test } from 'node:test';
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { LeaseCoordinator, LeaseNode, LeaseError } from '../src/index.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

test('获取租约并可动态续期，到期时间随续期延长', () => {
  const coordinator = new LeaseCoordinator();
  const lease = coordinator.acquire('orders', 'A', 100);
  assert.equal(lease.holderId, 'A');
  assert.equal(lease.epoch, 1);
  assert.equal(lease.renewalCount, 0);

  const renewed = coordinator.renew(lease.leaseId, 'A', 200);
  assert.equal(renewed.renewalCount, 1);
  assert.ok(renewed.expiresAt > lease.expiresAt, '续期后到期时间应延长');
});

test('资源被活跃租约占用时，其他节点无法重复获取', () => {
  const coordinator = new LeaseCoordinator();
  coordinator.acquire('orders', 'A', 1000);
  assert.throws(() => coordinator.acquire('orders', 'B', 1000), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'resource-locked';
  });
});

test('非持有者续期、租约过期后续期均被拒绝', () => {
  let fakeNow = 1_000;
  const coordinator = new LeaseCoordinator({ now: () => fakeNow });
  const lease = coordinator.acquire('orders', 'A', 100);

  assert.throws(() => coordinator.renew(lease.leaseId, 'B', 100), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'not-holder';
  });

  fakeNow += 150; // 越过到期时间
  assert.throws(() => coordinator.renew(lease.leaseId, 'A', 100), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'lease-expired';
  });
});

test('动态续期循环让租约存活远超初始 TTL', async () => {
  const coordinator = new LeaseCoordinator();
  const node = new LeaseNode('A', coordinator);
  node.acquire('orders', 60);

  await sleep(200); // 远超 60ms 初始 TTL

  const lease = node.lease;
  assert.ok(lease, '节点应仍持有租约');
  assert.ok(lease!.renewalCount >= 2, `应已多次续期，实际 ${lease!.renewalCount} 次`);
  assert.equal(coordinator.getLease('orders')!.state, 'active');
  node.dispose();
});

test('优雅移交：A 冻结写入，B 无缝继承剩余租约上下文，毫秒级切换', () => {
  const coordinator = new LeaseCoordinator();
  const nodeA = new LeaseNode('A', coordinator);
  const nodeB = new LeaseNode('B', coordinator);
  const acquired = nodeA.acquire('orders', 10_000);

  // 滚动重启指令到达，A 发起移交
  const start = performance.now();
  const pkg = nodeA.initiateHandover('B');
  assert.ok(nodeA.isWritesFrozen, 'A 应已冻结本地写入');
  assert.throws(() => nodeA.write('orders', 'x'), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'writes-frozen';
  });

  // 交接包携带剩余租约上下文
  assert.equal(pkg.fromNodeId, 'A');
  assert.equal(pkg.toNodeId, 'B');
  assert.equal(pkg.epoch, acquired.epoch + 1);
  assert.ok(pkg.remainingTtlMs > 9_000 && pkg.remainingTtlMs <= 10_000);
  assert.equal(pkg.expiresAt, acquired.expiresAt, '到期时间应原样继承');

  // B 接收交接包，立即接管主角色
  const inherited = nodeB.receiveHandover(pkg);
  const switchoverMs = performance.now() - start;

  assert.ok(nodeB.isPrimary);
  assert.ok(!nodeA.isPrimary, 'A 应已卸任');
  assert.equal(inherited.holderId, 'B');
  assert.equal(inherited.leaseId, acquired.leaseId, '租约 ID 传承不变');
  assert.equal(inherited.epoch, acquired.epoch + 1, 'fencing token 应递增');
  assert.equal(inherited.generation, 1);
  assert.equal(inherited.expiresAt, acquired.expiresAt, '剩余租约上下文无缝继承');
  assert.ok(switchoverMs < 20, `切换耗时应为毫秒级，实际 ${switchoverMs.toFixed(3)}ms`);

  // B 立即可写；A 的续期请求被拒绝
  nodeB.write('orders', 'first-write-after-handover');
  assert.throws(() => coordinator.renew(acquired.leaseId, 'A', 1000), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'not-holder';
  });
  nodeA.dispose();
  nodeB.dispose();
});

test('fencing：移交后旧 epoch 的写入被拒绝，防止脑裂双写', () => {
  const coordinator = new LeaseCoordinator();
  const nodeA = new LeaseNode('A', coordinator);
  const nodeB = new LeaseNode('B', coordinator);
  const acquired = nodeA.acquire('orders', 10_000);

  const pkg = nodeA.initiateHandover('B');
  nodeB.receiveHandover(pkg);

  assert.throws(
    () => coordinator.assertFencing('orders', acquired.epoch),
    (err: unknown) => err instanceof LeaseError && (err as LeaseError).code === 'fencing-token-stale',
  );
  assert.doesNotThrow(() => coordinator.assertFencing('orders', pkg.epoch));
  nodeA.dispose();
  nodeB.dispose();
});

test('移交发起后租约进入冻结态：续期被拒、重复发起被拒', () => {
  const coordinator = new LeaseCoordinator();
  const nodeA = new LeaseNode('A', coordinator);
  const acquired = nodeA.acquire('orders', 10_000);

  nodeA.initiateHandover('B');
  assert.equal(coordinator.getLease('orders')!.state, 'handover-pending');
  assert.throws(() => coordinator.renew(acquired.leaseId, 'A', 1000), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'handover-pending';
  });
  assert.throws(() => coordinator.initiateHandover(acquired.leaseId, 'A', 'C'), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'invalid-state';
  });
  nodeA.dispose();
});

test('移交可中止：候选人拒收时 A 恢复租约并解冻写入', () => {
  const coordinator = new LeaseCoordinator();
  const nodeA = new LeaseNode('A', coordinator);
  nodeA.acquire('orders', 10_000);

  nodeA.initiateHandover('B');
  nodeA.abortHandover();

  assert.equal(coordinator.getLease('orders')!.state, 'active');
  assert.ok(!nodeA.isWritesFrozen);
  assert.doesNotThrow(() => nodeA.write('orders', 'resumed'));
  nodeA.dispose();
});

test('非法移交被拒绝：过期租约、非持有者、候选人即持有者', () => {
  let fakeNow = 1_000;
  const coordinator = new LeaseCoordinator({ now: () => fakeNow });
  const lease = coordinator.acquire('orders', 'A', 100);

  assert.throws(() => coordinator.initiateHandover(lease.leaseId, 'B', 'C'), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'not-holder';
  });
  assert.throws(() => coordinator.initiateHandover(lease.leaseId, 'A', 'A'), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'invalid-argument';
  });

  fakeNow += 200; // 租约过期
  assert.throws(() => coordinator.initiateHandover(lease.leaseId, 'A', 'B'), (err: unknown) => {
    return err instanceof LeaseError && (err as LeaseError).code === 'lease-expired';
  });
});

test('伪造/篡改的交接包无法完成接管', () => {
  const coordinator = new LeaseCoordinator();
  const nodeA = new LeaseNode('A', coordinator);
  const nodeB = new LeaseNode('B', coordinator);
  nodeA.acquire('orders', 10_000);
  const pkg = nodeA.initiateHandover('B');

  assert.throws(
    () => nodeB.receiveHandover({ ...pkg, token: 'forged-token' }),
    (err: unknown) => err instanceof LeaseError && (err as LeaseError).code === 'invalid-token',
  );
  const nodeC = new LeaseNode('C', coordinator);
  assert.throws(
    () => nodeC.receiveHandover({ ...pkg, toNodeId: 'C' }),
    (err: unknown) => err instanceof LeaseError && (err as LeaseError).code === 'invalid-candidate',
  );

  // 合法交接包仍可完成
  nodeB.receiveHandover(pkg);
  assert.ok(nodeB.isPrimary);
  nodeA.dispose();
  nodeB.dispose();
  nodeC.dispose();
});
