import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";
import { Cluster, type RouteEvent } from "../src/cluster.ts";
import { NodeDrainingError, NodeNotFoundError } from "../src/errors.ts";

const QUIESCENT_MS = 5_000;

function makeCluster(ids: readonly string[]): Cluster {
  const cluster = new Cluster({ quiescentMs: QUIESCENT_MS });
  for (const id of ids) {
    cluster.addNode(id);
  }
  return cluster;
}

test("节点发起 Leave：进入 Leaving 状态并广播离开意向", (t: TestContext) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const cluster = makeCluster(["A", "B", "C", "D"]);

  const ok = cluster.getNode("C")!.initiateLeave();

  assert.equal(ok, true);
  assert.equal(cluster.getNode("C")!.lifecycle, "leaving");
  const intents = cluster
    .messageHistory()
    .filter((m) => m.type === "leave-intent");
  assert.equal(intents.length, 1);
  assert.deepEqual(intents[0], {
    type: "leave-intent",
    nodeId: "C",
    quiescentMs: QUIESCENT_MS,
    sentAt: intents[0]!.sentAt,
  });
  cluster.dispose();
});

test("重复 Leave 幂等：只广播一次离开意向", (t: TestContext) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const cluster = makeCluster(["A", "B"]);

  assert.equal(cluster.getNode("B")!.initiateLeave(), true);
  assert.equal(cluster.getNode("B")!.initiateLeave(), false);

  const intents = cluster
    .messageHistory()
    .filter((m) => m.type === "leave-intent");
  assert.equal(intents.length, 1);
  cluster.dispose();
});

test("其他节点收到广播后平滑将其移出就绪路由池", (t: TestContext) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const cluster = makeCluster(["A", "B", "C", "D"]);
  const leavingEvents: RouteEvent[] = [];
  cluster.events.on("leaving", (e: RouteEvent) => leavingEvents.push(e));

  cluster.getNode("C")!.initiateLeave();

  for (const id of ["A", "B", "D"]) {
    const pool = cluster.getNode(id)!.pool;
    assert.equal(pool.stateOf("C"), "leaving", `${id} 应观察到 C 处于平静期`);
    assert.equal(pool.isRoutable("C"), false, `${id} 的就绪池不应再包含 C`);
    assert.deepEqual(
      pool.readyNodes().sort(),
      ["A", "B", "D"].filter((x) => x !== id).sort(),
    );
  }
  assert.equal(leavingEvents.length, 3, "三个对等节点都应触发 leaving 事件");
  cluster.dispose();
});

test("平静期内新流量被立即阻断：结构化错误而非 RPC 超时", async (t: TestContext) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const cluster = makeCluster(["A", "B", "C"]);
  cluster.getNode("B")!.initiateLeave();

  const started = Date.now();
  await assert.rejects(cluster.rpc("A", "B", "ping"), (err: unknown) => {
    assert.ok(err instanceof NodeDrainingError);
    assert.equal(err.code, "ERR_NODE_DRAINING");
    assert.equal(err.nodeId, "B");
    return true;
  });
  const elapsed = Date.now() - started;
  assert.ok(elapsed < 50, `错误必须立即返回（实际 ${elapsed}ms），而非等待超时`);
  cluster.dispose();
});

test("平静期内进行中的请求正常完成（平滑排水，不中断在途流量）", async (t: TestContext) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const cluster = makeCluster(["A", "B"]);
  cluster.getNode("B")!.setRpcHandler(async (req) => {
    await new Promise((resolve) => setTimeout(resolve, 1_000));
    return { ack: req.payload };
  });

  const inflight = cluster.rpc("A", "B", "keep-me");
  cluster.getNode("B")!.initiateLeave();

  assert.equal(cluster.getNode("A")!.pool.inflightCount("B"), 1);
  t.mock.timers.tick(1_000);
  assert.deepEqual(await inflight, { ack: "keep-me" });
  assert.equal(cluster.getNode("A")!.pool.inflightCount("B"), 0);

  await assert.rejects(cluster.rpc("A", "B", "new-req"), NodeDrainingError);
  cluster.dispose();
});

test("平静期定时器超时后物理销毁路由条目，节点彻底下线", async (t: TestContext) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const cluster = makeCluster(["A", "B", "C"]);
  const removedEvents: RouteEvent[] = [];
  cluster.events.on("removed", (e: RouteEvent) => removedEvents.push(e));

  cluster.getNode("C")!.initiateLeave();
  t.mock.timers.tick(QUIESCENT_MS);

  assert.equal(cluster.getNode("C")!.lifecycle, "gone");
  for (const id of ["A", "B"]) {
    const pool = cluster.getNode(id)!.pool;
    assert.equal(pool.stateOf("C"), "removed", `${id} 应已物理移除 C 的条目`);
    assert.equal(pool.readyNodes().includes("C"), false);
  }
  assert.equal(removedEvents.length, 2, "两个对等节点都应触发 removed 事件");

  await assert.rejects(cluster.rpc("A", "C", "ping"), (err: unknown) => {
    assert.ok(err instanceof NodeNotFoundError);
    assert.equal((err as NodeNotFoundError).code, "ERR_NODE_NOT_FOUND");
    return true;
  });

  const completes = cluster
    .messageHistory()
    .filter((m) => m.type === "leave-complete");
  assert.equal(completes.length, 1, "离开节点应广播一次 leave-complete");
  cluster.dispose();
});

test("离开全过程无突发 RPC 超时：结果只有成功或即时的结构化错误", async (t: TestContext) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const cluster = makeCluster(["A", "B", "C"]);
  cluster.getNode("C")!.setRpcHandler(async (req) => {
    await new Promise((resolve) => setTimeout(resolve, 500));
    return req.payload;
  });

  // 离开前发出的请求 -> 进入排水流程
  const inflight = Array.from({ length: 5 }, (_, i) =>
    cluster.rpc("A", "C", `pre-${i}`),
  );
  cluster.getNode("C")!.initiateLeave();
  // 平静期内发出的请求 -> 立即被拒绝
  const blocked = Array.from({ length: 5 }, (_, i) =>
    cluster.rpc("A", "C", `post-${i}`),
  );

  t.mock.timers.tick(600); // 在途请求完成，仍处于平静期内
  const settled = await Promise.allSettled([...inflight, ...blocked]);

  const fulfilled = settled.filter((s) => s.status === "fulfilled");
  const rejected = settled.filter(
    (s): s is PromiseRejectedResult => s.status === "rejected",
  );
  assert.equal(fulfilled.length, 5, "在途请求应全部成功排水");
  assert.equal(rejected.length, 5, "平静期新请求应全部被即时拒绝");
  for (const r of rejected) {
    assert.ok(
      r.reason instanceof NodeDrainingError,
      `拒绝原因必须是即时结构化错误，而非超时: ${String(r.reason)}`,
    );
  }

  // 平静期结束后 -> 物理移除，错误类型切换为 NodeNotFoundError
  t.mock.timers.tick(QUIESCENT_MS);
  await assert.rejects(cluster.rpc("A", "C", "after"), NodeNotFoundError);

  // 就绪池流量重新路由到存活节点，全程无超时
  const ready = cluster.getNode("A")!.pool.readyNodes();
  assert.deepEqual(ready.sort(), ["B"]);
  assert.deepEqual(await cluster.rpc("A", "B", "rerouted"), {
    echoed: "rerouted",
    servedBy: "B",
  });
  cluster.dispose();
});
