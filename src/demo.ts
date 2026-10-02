import { ClusterNode } from "./node.ts";

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

async function main(): Promise<void> {
  const QUIESCENT_MS = 300;

  const nodeA = new ClusterNode("A", { quiescentMs: QUIESCENT_MS, rpcLatencyMs: 50 });
  const nodeB = new ClusterNode("B", { quiescentMs: QUIESCENT_MS });
  const nodeC = new ClusterNode("C", { quiescentMs: QUIESCENT_MS });
  nodeA.connect([nodeB, nodeC]);
  nodeB.connect([nodeA, nodeC]);
  nodeC.connect([nodeA, nodeB]);

  for (const node of [nodeA, nodeB, nodeC]) {
    node.on("state-change", (id, from, to) =>
      console.log(`[${node.id}] 路由条目 ${id}: ${from} -> ${to}`));
    node.on("route-removed", (id) =>
      console.log(`[${node.id}] 平静期结束，物理销毁路由条目 ${id}，就绪池=[${node.readyPool()}]`));
  }
  nodeB.on("leave-broadcast", (intent) =>
    console.log(`[${intent.nodeId}] 广播 LeaveIntent (epoch=${intent.epoch})`));

  console.log("== 初始就绪池 ==");
  console.log(`A: [${nodeA.readyPool()}]  B: [${nodeB.readyPool()}]  C: [${nodeC.readyPool()}]`);

  console.log("\n== B 上有一笔耗时 50ms 的在途 RPC ==");
  const inflight = nodeA.sendRpc("B", { id: "req-1", payload: "in-flight" });

  console.log("== B 主动发起优雅离开 ==");
  nodeB.initiateLeave();

  console.log(`A 就绪池(平静期内): [${nodeA.readyPool()}]  <- B 已被阻断新流量`);
  try {
    await nodeA.sendRpc("B", { id: "req-2", payload: "new-traffic" });
  } catch (err) {
    console.log(`A 向 B 发起新请求被快速拒绝: ${(err as Error).name}（无突发 RPC 超时）`);
  }

  const resp = await inflight;
  console.log(`在途请求 req-1 正常完成: ${JSON.stringify(resp)}`);

  console.log(`\n== 等待平静期 ${QUIESCENT_MS}ms 超时 ==`);
  await sleep(QUIESCENT_MS + 100);

  console.log(`\n最终就绪池  A: [${nodeA.readyPool()}]  C: [${nodeC.readyPool()}]`);
  console.log(`A 路由表中是否仍存在 B: ${nodeA.getRouteEntry("B") !== undefined}`);
}

await main();
