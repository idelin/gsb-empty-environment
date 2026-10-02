/**
 * 运行样例：1000 个边缘工作节点 / 5 个区域网关 / 1 个根协调器。
 * 模拟 70 秒：t=30s 时 200 个边缘节点掉线，观察根协调器负载变化。
 *
 * 运行：node examples/sample.ts   （Node >= 22.6，原生 TypeScript）
 */
import { ManualClock } from "../src/clock.ts";
import { RootCoordinator } from "../src/root-coordinator.ts";
import { RegionGateway } from "../src/region-gateway.ts";
import { WorkerNode } from "../src/worker-node.ts";

const GATEWAYS = 5;
const WORKERS = 1000;
const QUOTA_PER_WORKER = 1;
const TICK_MS = 100;
const DURATION_MS = 70_000;
const DROP_AT_MS = 30_000;
const DROP_COUNT = 200;

const clock = new ManualClock();
const root = new RootCoordinator(clock, { leaseTtlMs: 60_000, totalQuota: 100_000 });

const gateways: RegionGateway[] = [];
for (let g = 0; g < GATEWAYS; g++) {
  gateways.push(
    new RegionGateway(`region-gw-${g}`, root, clock, {
      subLeaseTtlMs: 5_000,
      quotaPool: 5_000,
    }),
  );
}

const workers: WorkerNode[] = [];
for (let w = 0; w < WORKERS; w++) {
  const gateway = gateways[w % GATEWAYS];
  workers.push(new WorkerNode(`worker-${w}`, gateway, clock, QUOTA_PER_WORKER));
}

interface WindowStat {
  label: string;
  rootMessages: number;
  rootOps: number;
  activeSubLeases: number;
}

const windows: WindowStat[] = [];
let windowLabel = "t=0s ~ t=30s（稳态：1000 节点在线）";
let dropped = false;

const snapshotRoot = () => root.getMetrics();
let prev = snapshotRoot();

const bucketMs = 10_000;
const rootOpsTimeline: number[] = [];
let bucketPrev = snapshotRoot();

for (let t = 0; t <= DURATION_MS; t += TICK_MS) {
  for (const worker of workers) worker.tick();
  for (const gateway of gateways) gateway.sweep();
  root.sweep();
  clock.advance(TICK_MS);

  if (clock.now() % bucketMs === 0) {
    const cur = snapshotRoot();
    rootOpsTimeline.push(cur.leaseOperations - bucketPrev.leaseOperations);
    bucketPrev = cur;
  }

  if (!dropped && clock.now() >= DROP_AT_MS) {
    for (let i = 0; i < DROP_COUNT; i++) workers[i].drop();
    dropped = true;
    const cur = snapshotRoot();
    windows.push({
      label: windowLabel,
      rootMessages: cur.messagesHandled - prev.messagesHandled,
      rootOps: cur.leaseOperations - prev.leaseOperations,
      activeSubLeases: gateways.reduce((s, g) => s + g.activeSubLeaseCount(), 0),
    });
    prev = cur;
    windowLabel = `t=30s ~ t=70s（${DROP_COUNT} 节点掉线后）`;
  }
}

const finalMetrics = snapshotRoot();
windows.push({
  label: windowLabel,
  rootMessages: finalMetrics.messagesHandled - prev.messagesHandled,
  rootOps: finalMetrics.leaseOperations - prev.leaseOperations,
  activeSubLeases: gateways.reduce((s, g) => s + g.activeSubLeaseCount(), 0),
});

console.log("=== 分层租约协调器仿真 ===");
console.log(`规模: ${WORKERS} 工作节点 / ${GATEWAYS} 区域网关 / 1 根协调器`);
console.log(`租约: 根租约 TTL=${root.leaseTtlMs / 1000}s, 子租约 TTL=${gateways[0].subLeaseTtlMs / 1000}s`);
console.log("");
console.log("--- 分窗口根中心负载 ---");
for (const w of windows) {
  console.log(
    `${w.label}\n  根中心消息数=${w.rootMessages}, 根中心操作数=${w.rootOps}, 活跃子租约=${w.activeSubLeases}`,
  );
}
console.log("");
console.log("--- 验收指标 ---");
console.log(
  `根中心每 10s 操作数时间线: [${rootOpsTimeline.join(", ")}]（t=30s 掉线前后无尖峰）`,
);
console.log(`根中心通信对端: [${root.getPeers().join(", ")}]`);
console.log(`根中心活跃租约数: ${root.activeLeaseCount()}（应等于网关数 ${GATEWAYS}）`);
console.log(`根中心全程消息总数: ${finalMetrics.messagesHandled}`);
console.log(`在线节点持有有效子租约: ${workers.filter((w) => w.isOnline() && w.hasValidSubLease()).length}/${WORKERS - DROP_COUNT}`);
console.log(`掉线节点子租约残留: ${workers.slice(0, DROP_COUNT).filter((w) => w.hasValidSubLease()).length}（应为 0，已本地超时释放）`);
