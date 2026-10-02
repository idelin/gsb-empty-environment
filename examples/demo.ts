import { ManualClock, AdjustableClock } from "../src/clock.ts";
import { ClockDriftMonitor } from "../src/drift-monitor.ts";
import { LeaseManager, LeaseViolationError, FencingTokenIssuer } from "../src/lease-manager.ts";
import { FencedStore, StaleFencingTokenError } from "../src/fenced-store.ts";

const LEASE_TTL_MS = 10_000;
const MAX_DRIFT_MS = 500;

function makeNode(name: string, monotonic: ManualClock, issuer?: FencingTokenIssuer) {
  const wall = new AdjustableClock(monotonic);
  const monitor = new ClockDriftMonitor(wall, monotonic, MAX_DRIFT_MS);
  const lease = new LeaseManager(
    { leaseDurationMs: LEASE_TTL_MS, maxClockDriftMs: MAX_DRIFT_MS, tokenIssuer: issuer },
    wall,
    monitor,
  );
  return { name, wall, monitor, lease };
}

type Node = ReturnType<typeof makeNode>;

function tryWrite(node: Node, op: string, atMs: number): void {
  try {
    const receipt = node.lease.write(op);
    console.log(
      `t=${String(atMs).padStart(5)}ms  [${node.name}] write("${op}") 允许  ` +
        `(fencing token=${receipt.fencingToken}, 租约已持有 ${receipt.elapsedMs}ms)`,
    );
  } catch (err) {
    if (err instanceof LeaseViolationError) {
      console.log(
        `t=${String(atMs).padStart(5)}ms  [${node.name}] write("${op}") 拒绝  ` +
          `(role=${err.status.role}, reason=${err.status.demotionReason})`,
      );
    } else {
      throw err;
    }
  }
}

console.log("=== 分布式租约管理器 + 时钟漂移容忍器：验收场景演示 ===");
console.log(`配置: 租约 TTL=${LEASE_TTL_MS}ms, 最大时钟漂移=${MAX_DRIFT_MS}ms, RTT 余量=0ms`);
console.log(`=> 安全写窗口 = ${LEASE_TTL_MS} - ${MAX_DRIFT_MS} - 0 = ${LEASE_TTL_MS - MAX_DRIFT_MS}ms\n`);

{
  console.log("[场景 1] 正常运行：安全期（9.5s）过后主节点主动放弃特权");
  const monotonic = new ManualClock(0);
  const node = makeNode("node-A", monotonic);
  node.lease.onDemote((reason, token) =>
    console.log(`           [${node.name}] >>> 降级事件: reason=${reason}, token=${token}`),
  );
  const token = node.lease.acquire();
  console.log(`t=    0ms  [node-A] acquire() -> role=primary, fencing token=${token}`);

  monotonic.advance(9_000);
  tryWrite(node, "op-1", 9_000);

  monotonic.advance(499);
  tryWrite(node, "op-2", 9_499);

  monotonic.advance(1);
  tryWrite(node, "op-3", 9_500);
  console.log();
}

{
  console.log("[场景 2] 注入时钟前拨 1 秒突变：安全自检触发紧急提前降级");
  const monotonic = new ManualClock(0);
  const node = makeNode("node-B", monotonic);
  node.lease.onDemote((reason, token) =>
    console.log(`           [${node.name}] >>> 紧急降级事件: reason=${reason}, token=${token}`),
  );
  const token = node.lease.acquire();
  console.log(`t=    0ms  [node-B] acquire() -> role=primary, fencing token=${token}`);

  monotonic.advance(8_000);
  tryWrite(node, "op-a", 8_000);

  console.log("t= 8000ms  [node-B] !! 注入时钟突变: 前拨 +1000ms（超过 500ms 漂移上限）");
  node.wall.adjust(1_000);
  console.log("           [node-B] 安全自检: 观测时钟阶跃 1000ms > 容忍 500ms -> 漂移假设被破坏");

  tryWrite(node, "op-b", 8_000);
  console.log();
}

{
  console.log("[场景 3] 脑裂防护：fencing token 让存储层拒绝旧主的写");
  const monotonic = new ManualClock(0);
  const store = new FencedStore();
  const issuer = new FencingTokenIssuer();
  const nodeA = makeNode("node-A", monotonic, issuer);
  const tokenA = nodeA.lease.acquire();
  nodeA.lease.write("A-op-1");
  store.write(tokenA, "balance", "100");
  console.log(`t=    0ms  [node-A] 持 token=${tokenA} 写入 balance=100 -> 存储接受`);

  monotonic.advance(8_000);
  console.log("t= 8000ms  [node-A] !! 时钟突变 +1000ms，node-A 被紧急降级（但假设它仍试图写）");
  nodeA.wall.adjust(1_000);

  const nodeB = makeNode("node-B", monotonic, issuer);
  const tokenB = nodeB.lease.acquire();
  nodeB.lease.write("B-op-1");
  store.write(tokenB, "balance", "200");
  console.log(`t= 8000ms  [node-B] 新主持 token=${tokenB} 写入 balance=200 -> 存储接受`);

  try {
    store.write(tokenA, "balance", "999");
  } catch (err) {
    if (err instanceof StaleFencingTokenError) {
      console.log(
        `t= 8000ms  [node-A] 旧主用 token=${tokenA} 写入 balance=999 -> 存储拒绝: ${err.message}`,
      );
    } else {
      throw err;
    }
  }
  console.log(`           最终 balance=${store.read("balance")}（旧主的脏写被拦截，无双主）`);
}
