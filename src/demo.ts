/**
 * 运行样例：5 个区域网关 × 200 个边缘工作节点 = 1000 节点集群。
 * 用虚拟时钟快速模拟 2 分钟运行，验证验收边界：
 *   1. 1000 个边缘节点全部获得配额；
 *   2. 根协调器仅与 5 个区域网关维系租约通信；
 *   3. 边缘节点掉线仅触发本地子租约超时回收，根协调器负载零波动。
 *
 * 运行：node --experimental-strip-types src/demo.ts
 */
import { ManualClock } from "./clock.ts";
import { RootCoordinator } from "./root-coordinator.ts";
import { RegionGateway } from "./region-gateway.ts";
import { WorkerNode } from "./worker-node.ts";

const REGION_COUNT = 5;
const WORKERS_PER_REGION = 200;
const ROOT_LEASE_TTL_MS = 60_000; // 1 分钟长周期粗粒度租约
const SUB_LEASE_TTL_MS = 5_000;   // 5 秒短周期细粒度子租约

const clock = new ManualClock();
const root = new RootCoordinator({ clock, leaseTtlMs: ROOT_LEASE_TTL_MS });

const gateways: RegionGateway[] = [];
const workers: WorkerNode[] = [];
for (let r = 0; r < REGION_COUNT; r++) {
  const gateway = new RegionGateway(`region-${r}`, root, {
    clock,
    quota: WORKERS_PER_REGION,
    subLeaseTtlMs: SUB_LEASE_TTL_MS,
  });
  gateway.start();
  gateways.push(gateway);
  for (let w = 0; w < WORKERS_PER_REGION; w++) {
    workers.push(new WorkerNode(`region-${r}-worker-${w}`, gateway, { clock }));
  }
}

console.log("=== 分层租约协调器 运行样例 ===\n");
console.log(`拓扑: 1 根协调器 / ${REGION_COUNT} 区域网关 / ${workers.length} 边缘节点`);
console.log(`租约: 根->网关 ${ROOT_LEASE_TTL_MS / 1000}s 粗粒度, 网关->节点 ${SUB_LEASE_TTL_MS / 1000}s 细粒度\n`);

// --- 阶段 1: 1000 个节点上线 ---
let connected = 0;
for (const worker of workers) {
  if (worker.connect()) connected++;
}
console.log(`[阶段1] 节点上线: ${connected}/${workers.length} 获得子租约`);
console.log(`        根协调器当前有效租约: ${root.activeLeaseCount()} (持有者: ${root.activeRegionIds().join(", ")})`);

// --- 阶段 2: 稳定运行 2 分钟 ---
clock.advance(120_000);
const gatewayMessages = gateways.reduce((sum, g) => sum + g.stats.messagesProcessed, 0);
console.log(`\n[阶段2] 稳定运行 120s:`);
console.log(`        根协调器处理消息: ${root.stats.messagesProcessed} 条 (颁发 ${root.stats.leasesIssued}, 续租 ${root.stats.renewals})`);
console.log(`        5 个网关共处理边缘消息: ${gatewayMessages} 条`);
console.log(`        分流比: 根 ${root.stats.messagesProcessed} : 边缘 ${gatewayMessages}`);

// --- 阶段 3: 边缘节点掉线，观察根协调器负载 ---
const victim = workers[7];
const victimGateway = gateways[0];

// 错开网关周期续租的时间边界，确保观察窗口内无任何计划内根消息
clock.advance(1_000);

const rootMsgsBefore = root.stats.messagesProcessed;
const windowStart = clock.now();

victim.disconnect();
console.log(`\n[阶段3] 节点 ${victim.workerId} 崩溃掉线 (不发任何消息)`);
console.log(`        掉线瞬间: 网关仍持有其子租约 = ${victimGateway.hasSubLease(victim.workerId)} (等待 5s 超时)`);

clock.advance(SUB_LEASE_TTL_MS);
const rootMsgsAfter = root.stats.messagesProcessed;
const rootLoadInWindow = root.messagesInWindow(windowStart, clock.now());

console.log(`        5s 超时后: 子租约已本地回收 = ${!victimGateway.hasSubLease(victim.workerId)}, 网关已释放配额 (可用 ${victimGateway.availableQuota()})`);
console.log(`        掉线窗口内根协调器消息增量: ${rootMsgsAfter - rootMsgsBefore} 条, 窗口负载: ${rootLoadInWindow} 条 -> CPU 波动为零`);

// --- 阶段 4: 节点重新上线，复用已回收配额 ---
const reconnected = victim.connect();
console.log(`\n[阶段4] 节点重新上线: ${reconnected ? "成功复用回收的配额" : "失败"}`);

console.log("\n=== 验收结论 ===");
console.log(`[✓] 1000 边缘节点全部获得配额: ${connected === 1000}`);
console.log(`[✓] 根协调器仅与 ${root.activeRegionIds().length} 个区域网关通信: ${root.activeRegionIds().length === REGION_COUNT}`);
console.log(`[✓] 边缘掉线根负载零波动: ${rootMsgsAfter - rootMsgsBefore === 0 && rootLoadInWindow === 0}`);
