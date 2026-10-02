/**
 * 验收边界测试：
 *  1. 1000 个边缘工作节点请求配额，全部获得子租约；
 *  2. 根协调器仅与 5 个区域网关维系租约通信；
 *  3. 边缘节点掉线仅影响本地子租约超时释放，根协调中心 CPU 负载近乎零波动。
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ManualClock } from "../src/clock.ts";
import { RootCoordinator } from "../src/root-coordinator.ts";
import { RegionGateway } from "../src/region-gateway.ts";
import { WorkerNode } from "../src/worker-node.ts";

const REGION_COUNT = 5;
const WORKERS_PER_REGION = 200;
const TOTAL_WORKERS = REGION_COUNT * WORKERS_PER_REGION; // 1000

function buildCluster() {
  const clock = new ManualClock();
  const root = new RootCoordinator({ clock, leaseTtlMs: 60_000 });
  const gateways: RegionGateway[] = [];
  const workers: WorkerNode[] = [];
  for (let r = 0; r < REGION_COUNT; r++) {
    const gateway = new RegionGateway(`region-${r}`, root, {
      clock,
      quota: WORKERS_PER_REGION,
      subLeaseTtlMs: 5_000,
    });
    gateway.start();
    gateways.push(gateway);
    for (let w = 0; w < WORKERS_PER_REGION; w++) {
      workers.push(new WorkerNode(`region-${r}-worker-${w}`, gateway, { clock }));
    }
  }
  return { clock, root, gateways, workers };
}

describe("验收边界 1: 1000 个边缘工作节点请求配额", () => {
  it("全部 1000 个节点获得子租约，配额按区域隔离", () => {
    const { gateways, workers } = buildCluster();
    for (const worker of workers) {
      assert.equal(worker.connect(), true, `${worker.workerId} 应获得子租约`);
    }
    for (const gateway of gateways) {
      assert.equal(gateway.activeSubLeaseCount(), WORKERS_PER_REGION);
      assert.equal(gateway.availableQuota(), 0);
    }
    // 超额请求被拒绝：配额由根租约粗粒度约束
    const extra = new WorkerNode("region-0-worker-extra", gateways[0], {
      clock: (gateways[0] as unknown as { clock: ManualClock }).clock,
    });
    assert.equal(extra.connect(), false);
  });
});

describe("验收边界 2: 根协调器仅与 5 个区域网关维系租约通信", () => {
  it("根侧有效租约恒为 5 个区域，长周期运行根消息量远低于边缘", () => {
    const { clock, root, gateways, workers } = buildCluster();
    for (const worker of workers) worker.connect();

    assert.deepEqual(root.activeRegionIds(), [
      "region-0", "region-1", "region-2", "region-3", "region-4",
    ]);
    assert.equal(root.activeLeaseCount(), REGION_COUNT);

    // 稳定运行 2 分钟
    clock.advance(120_000);
    assert.equal(root.activeLeaseCount(), REGION_COUNT);

    const gatewayMessages = gateways.reduce((s, g) => s + g.stats.messagesProcessed, 0);
    // 根只处理 5 个网关的颁发+周期续租（每 30s 一次），与 1000 节点规模无关
    assert.equal(root.stats.leasesIssued, REGION_COUNT);
    assert.ok(root.stats.messagesProcessed <= REGION_COUNT * 5,
      `根消息 ${root.stats.messagesProcessed} 应只来自网关周期续租`);
    // 边缘流量被网关完全吸收：分流比 > 1000:1
    assert.ok(gatewayMessages / root.stats.messagesProcessed > 1000,
      `分流比 ${gatewayMessages}:${root.stats.messagesProcessed}`);
  });
});

describe("验收边界 3: 边缘节点掉线只影响本地子租约", () => {
  it("掉线后 5s 本地超时回收，根协调器负载零波动", () => {
    const { clock, root, gateways, workers } = buildCluster();
    for (const worker of workers) worker.connect();
    clock.advance(120_000); // 进入稳态

    // 错开网关续租边界，取一段无任何计划内根消息的观察窗口
    clock.advance(1_000);
    const victim = workers[0];
    const gateway = gateways[0];
    const rootMsgsBefore = root.stats.messagesProcessed;
    const windowStart = clock.now();

    victim.disconnect(); // 模拟崩溃：不发任何消息
    assert.equal(gateway.hasSubLease(victim.workerId), true, "超时前子租约仍挂账");

    clock.advance(5_000); // 子租约 TTL 到期

    // 本地回收：配额归还区域池，其他节点不受影响
    assert.equal(gateway.hasSubLease(victim.workerId), false);
    assert.equal(gateway.availableQuota(), 1);
    assert.equal(gateway.stats.subLeaseExpirations, 1);
    assert.equal(gateway.activeSubLeaseCount(), WORKERS_PER_REGION - 1);

    // 根协调中心 CPU 负载零波动：窗口内消息增量为 0
    assert.equal(root.stats.messagesProcessed - rootMsgsBefore, 0);
    assert.equal(root.messagesInWindow(windowStart, clock.now()), 0);

    // 回收的配额可被新节点立即复用
    const replacement = new WorkerNode("region-0-worker-new", gateway, { clock });
    assert.equal(replacement.connect(), true);
  });

  it("大规模 churn（200 节点同时掉线）也不传导到根协调器", () => {
    const { clock, root, gateways, workers } = buildCluster();
    for (const worker of workers) worker.connect();
    clock.advance(121_000); // 稳态，且错开续租边界

    const rootMsgsBefore = root.stats.messagesProcessed;
    // region-0 全部 200 个节点同时崩溃
    for (let w = 0; w < WORKERS_PER_REGION; w++) workers[w].disconnect();
    clock.advance(10_000); // 覆盖 5s 子租约超时

    assert.equal(gateways[0].activeSubLeaseCount(), 0);
    assert.equal(gateways[0].stats.subLeaseExpirations, WORKERS_PER_REGION);
    assert.equal(gateways[0].availableQuota(), WORKERS_PER_REGION);
    // 其他区域不受影响
    for (let r = 1; r < REGION_COUNT; r++) {
      assert.equal(gateways[r].activeSubLeaseCount(), WORKERS_PER_REGION);
    }
    // 根协调器在此期间处理的消息为零
    assert.equal(root.stats.messagesProcessed - rootMsgsBefore, 0);
  });
});
