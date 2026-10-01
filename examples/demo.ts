import { Cluster, type RouteEvent } from "../src/cluster.ts";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const t0 = Date.now();
const stamp = () => `t+${String(Date.now() - t0).padStart(5, " ")}ms`;

const QUIESCENT_MS = 2_000;
const cluster = new Cluster({ quiescentMs: QUIESCENT_MS });
for (const id of ["node-a", "node-b", "node-c", "node-d"]) {
  cluster.addNode(id);
}

cluster.events.on("leaving", (e: RouteEvent) =>
  console.log(`${stamp()}  [${e.observer}] 收到离开广播 -> 将 ${e.peer} 移出就绪路由池（进入平静期）`),
);
cluster.events.on("removed", (e: RouteEvent) =>
  console.log(`${stamp()}  [${e.observer}] 平静期结束 -> 物理销毁 ${e.peer} 的路由条目`),
);
cluster.events.on("gone", (e: { peer: string }) =>
  console.log(`${stamp()}  [cluster] ${e.peer} 已彻底下线 (gone)`),
);

// node-c 模拟一个 1.2s 的慢请求，用于演示“在途请求排水”
cluster.getNode("node-c")!.setRpcHandler(async (req) => {
  await sleep(1_200);
  return { servedBy: "node-c", payload: req.payload };
});

async function tryRpc(from: string, to: string, payload: string): Promise<void> {
  try {
    const res = await cluster.rpc(from, to, payload);
    console.log(`${stamp()}  RPC ${from} -> ${to} 成功: ${JSON.stringify(res)}`);
  } catch (err) {
    const code = (err as { code?: string }).code ?? "UNKNOWN";
    console.log(`${stamp()}  RPC ${from} -> ${to} 被即时拒绝 [${code}]（无超时等待）`);
  }
}

console.log("=== 阶段 1: 正常流量 ===");
await tryRpc("node-a", "node-c", "req-1");

console.log("\n=== 阶段 2: node-c 主动发起 Leave 并广播离开意向 ===");
const inflight = cluster.rpc("node-a", "node-c", "slow-inflight-req");
await sleep(100);
cluster.getNode("node-c")!.initiateLeave();
console.log(`${stamp()}  [node-c] 已广播 leave-intent，进入 Leaving 状态`);

console.log("\n--- 平静期内：新流量被阻断，在途请求继续排水 ---");
await tryRpc("node-b", "node-c", "new-req-during-quiescent");
const rerouted = cluster.getNode("node-b")!.pool.readyNodes()[0]!;
console.log(`${stamp()}  [node-b] 就绪池重路由到 ${rerouted}`);
await tryRpc("node-b", rerouted, "rerouted-req");
console.log(`${stamp()}  等待在途慢请求完成...`);
const inflightResult = await inflight;
console.log(`${stamp()}  在途请求结果: ${JSON.stringify(inflightResult)}`);

console.log(`\n=== 阶段 3: 等待平静期 (${QUIESCENT_MS}ms) 定时器超时 ===`);
await sleep(QUIESCENT_MS + 200);

console.log("\n--- 物理移除后：路由条目已销毁 ---");
await tryRpc("node-b", "node-c", "after-removal");

console.log("\n=== 最终路由表 ===");
for (const id of ["node-a", "node-b", "node-d"]) {
  const ready = cluster.getNode(id)!.pool.readyNodes();
  console.log(`  [${id}] ready -> ${ready.join(", ")}`);
}
console.log(`  [node-c] lifecycle -> ${cluster.getNode("node-c")!.lifecycle}`);

cluster.dispose();
