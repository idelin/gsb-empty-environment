import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock } from "../src/clock.ts";
import { RootCoordinator } from "../src/root-coordinator.ts";
import { RegionGateway } from "../src/region-gateway.ts";
import { WorkerNode } from "../src/worker-node.ts";

function makeCluster(options: {
  gateways?: number;
  rootTtlMs?: number;
  subTtlMs?: number;
  quotaPool?: number;
  totalQuota?: number;
} = {}) {
  const clock = new ManualClock();
  const root = new RootCoordinator(clock, {
    leaseTtlMs: options.rootTtlMs ?? 60_000,
    totalQuota: options.totalQuota ?? 1_000_000,
  });
  const gateways: RegionGateway[] = [];
  const n = options.gateways ?? 5;
  for (let i = 0; i < n; i++) {
    gateways.push(
      new RegionGateway(`region-gw-${i}`, root, clock, {
        subLeaseTtlMs: options.subTtlMs ?? 5_000,
        quotaPool: options.quotaPool ?? 5_000,
      }),
    );
  }
  return { clock, root, gateways };
}

test("根协调器向区域网关颁发 60s 粗粒度租约，续租延长一个周期", () => {
  const { clock, root } = makeCluster();
  const granted = root.requestLease("region-gw-0", 1000);
  assert.equal(granted.ok, true);
  if (!granted.ok) return;
  assert.equal(granted.lease.expiresAt - granted.lease.issuedAt, 60_000);

  clock.advance(40_000);
  const leaseId = granted.lease.leaseId;
  const versionBefore = granted.lease.version;
  const renewed = root.renewLease(leaseId, versionBefore);
  assert.equal(renewed.ok, true);
  if (!renewed.ok) return;
  assert.equal(renewed.lease.expiresAt, 40_000 + 60_000);
  assert.equal(renewed.lease.version, versionBefore + 1);
});

test("根租约过期后被 sweep 回收，配额归还根池", () => {
  const { clock, root } = makeCluster({ totalQuota: 1000 });
  const granted = root.requestLease("region-gw-0", 1000);
  assert.equal(granted.ok, true);

  const rejected = root.requestLease("region-gw-1", 1);
  assert.equal(rejected.ok, false, "配额耗尽应拒绝");

  clock.advance(61_000);
  const expired = root.sweep();
  assert.deepEqual(expired, [granted.ok ? granted.lease.leaseId : ""]);
  assert.equal(root.activeLeaseCount(), 0);

  const again = root.requestLease("region-gw-1", 1000);
  assert.equal(again.ok, true, "回收后配额可再分配");
});

test("区域网关向工作节点颁发 5s 细粒度子租约并做配额记账", () => {
  const { gateways } = makeCluster({ quotaPool: 3 });
  const gw = gateways[0];
  const r1 = gw.requestSubLease("worker-1", 1);
  const r2 = gw.requestSubLease("worker-2", 1);
  const r3 = gw.requestSubLease("worker-3", 1);
  assert.equal(r1.ok && r2.ok && r3.ok, true);
  if (r1.ok) assert.equal(r1.subLease.expiresAt - r1.subLease.issuedAt, 5_000);

  const r4 = gw.requestSubLease("worker-4", 1);
  assert.equal(r4.ok, false, "区域配额池耗尽应拒绝");
  assert.equal(gw.activeSubLeaseCount(), 3);
});

test("子租约续租成功；过期子租约续租被拒绝并回收", () => {
  const { clock, gateways } = makeCluster();
  const gw = gateways[0];
  const granted = gw.requestSubLease("worker-1", 1);
  assert.equal(granted.ok, true);
  if (!granted.ok) return;

  clock.advance(3_000);
  const renewed = gw.renewSubLease(granted.subLease.subLeaseId, granted.subLease.version);
  assert.equal(renewed.ok, true);
  if (!renewed.ok) return;
  assert.equal(renewed.subLease.expiresAt, 3_000 + 5_000);

  clock.advance(6_000); // t=9s，子租约已过期
  const late = gw.renewSubLease(granted.subLease.subLeaseId, renewed.subLease.version);
  assert.equal(late.ok, false);
  assert.equal(gw.activeSubLeaseCount(), 0);
});

test("验收：1000 个工作节点获得配额，根中心仅与 5 个网关通信", () => {
  const { clock, root, gateways } = makeCluster();
  const workers: WorkerNode[] = [];
  for (let i = 0; i < 1000; i++) {
    workers.push(new WorkerNode(`worker-${i}`, gateways[i % 5], clock, 1));
  }

  for (let t = 0; t < 20_000; t += 100) {
    for (const w of workers) w.tick();
    for (const g of gateways) g.sweep();
    root.sweep();
    clock.advance(100);
  }

  const granted = workers.filter((w) => w.hasValidSubLease()).length;
  assert.equal(granted, 1000, "1000 个节点均应持有有效子租约");

  const peers = root.getPeers();
  assert.equal(peers.length, 5, "根中心通信对端应只有 5 个区域网关");
  assert.ok(peers.every((p) => p.startsWith("region-gw-")), "对端不含任何工作节点");
  assert.equal(root.activeLeaseCount(), 5, "根租约表仅 5 条");

  const metrics = root.getMetrics();
  assert.ok(
    metrics.messagesHandled <= 10,
    `根中心 20s 内消息数应极少（实际 ${metrics.messagesHandled}），与 1000 节点规模解耦`,
  );
});

test("验收：边缘节点掉线仅本地子租约超时释放，根中心负载零波动", () => {
  const { clock, root, gateways } = makeCluster();
  const workers: WorkerNode[] = [];
  for (let i = 0; i < 1000; i++) {
    workers.push(new WorkerNode(`worker-${i}`, gateways[i % 5], clock, 1));
  }

  // 稳态运行 10s
  for (let t = 0; t < 10_000; t += 100) {
    for (const w of workers) w.tick();
    for (const g of gateways) g.sweep();
    root.sweep();
    clock.advance(100);
  }
  const before = root.getMetrics();
  const subBefore = gateways.reduce((s, g) => s + g.activeSubLeaseCount(), 0);
  assert.equal(subBefore, 1000);

  // 200 个节点掉线
  for (let i = 0; i < 200; i++) workers[i].drop();

  // 继续运行 10s（跨过子租约 TTL=5s）
  for (let t = 0; t < 10_000; t += 100) {
    for (const w of workers) w.tick();
    for (const g of gateways) g.sweep();
    root.sweep();
    clock.advance(100);
  }

  const after = root.getMetrics();
  assert.equal(
    after.messagesHandled - before.messagesHandled,
    0,
    "掉线及子租约超时窗口内根中心不应收到任何消息",
  );
  assert.equal(
    after.leaseOperations - before.leaseOperations,
    0,
    "根中心操作数零增长（CPU 负载近乎零波动）",
  );

  const subAfter = gateways.reduce((s, g) => s + g.activeSubLeaseCount(), 0);
  assert.equal(subAfter, 800, "掉线的 200 个子租约应在网关侧超时释放");
  assert.equal(root.activeLeaseCount(), 5, "根租约层完全不受边缘抖动影响");
});

test("根租约过期未续时网关自动重新申请，子租约服务不中断", () => {
  const { clock, root, gateways } = makeCluster();
  const gw = gateways[0];
  const first = gw.requestSubLease("worker-1", 1);
  assert.equal(first.ok, true);
  assert.equal(root.activeLeaseCount(), 1);

  // 网关不续租，根租约过期被回收
  clock.advance(61_000);
  root.sweep();
  assert.equal(root.activeLeaseCount(), 0);

  // 新节点到来触发网关重新申请根租约
  const second = gw.requestSubLease("worker-2", 1);
  assert.equal(second.ok, true);
  assert.equal(root.activeLeaseCount(), 1);
});
