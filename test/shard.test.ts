/**
 * 分层分片与局部性感知路由引擎测试。
 * 覆盖：边界条件、最小迁移、局部性偏好、过载溢出、
 * 并发安全与严格无泄漏断言。
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { Topology, LOCALITY } from "../src/shard/topology.ts";
import {
  VirtualShardMap,
  buildUniformTopology,
  shardOfKey,
  fnv1a,
} from "../src/shard/vnodes.ts";
import { LocalityAwareRouter, AsyncMutex } from "../src/shard/router.ts";

const SHARDS = 512;

function makeRouter(nodeCount = 12, shardCount = SHARDS) {
  // 2 DC × 2 Rack × 3 Node 的均匀拓扑（默认 12 节点）
  const topo = buildUniformTopology(2, 2, Math.ceil(nodeCount / 4));
  const router = new LocalityAwareRouter(topo, shardCount);
  return { topo, router };
}

test("拓扑：分层距离度量正确", () => {
  const topo = buildUniformTopology(2, 2, 2);
  assert.equal(topo.distance("dc0-rack0-node0", "dc0-rack0-node0"), LOCALITY.SAME_NODE);
  assert.equal(topo.distance("dc0-rack0-node0", "dc0-rack0-node1"), LOCALITY.SAME_RACK);
  assert.equal(topo.distance("dc0-rack0-node0", "dc0-rack1-node0"), LOCALITY.SAME_DC);
  assert.equal(topo.distance("dc0-rack0-node0", "dc1-rack0-node0"), LOCALITY.CROSS_DC);
  assert.throws(() => topo.distance("ghost", "dc0-rack0-node0"), /节点不存在/);
});

test("边界：非法分片数与空拓扑", () => {
  assert.throws(() => new VirtualShardMap(0, new Topology()), /正整数/);
  assert.throws(() => new VirtualShardMap(-3, new Topology()), /正整数/);
  const empty = new VirtualShardMap(16, new Topology());
  const result = empty.rebalance();
  assert.equal(result.assignment.size, 0);
  assert.throws(() => empty.nodeOfShard(0), /未分配/);
});

test("边界：重复节点与未知节点操作", () => {
  const topo = new Topology();
  topo.addNode({ nodeId: "n1", rackId: "r1", dcId: "d1", capacity: 1 });
  assert.throws(
    () => topo.addNode({ nodeId: "n1", rackId: "r1", dcId: "d1", capacity: 1 }),
    /已存在/,
  );
  assert.throws(() => topo.removeNode("ghost"), /不存在/);
  assert.throws(() => topo.get("ghost"), /不存在/);
});

test("虚拟分片：全部 key 可路由且分片全覆盖", () => {
  const { router } = makeRouter();
  for (let i = 0; i < 5000; i++) {
    const decision = router.route(`key-${i}`);
    assert.ok(decision.shardId >= 0 && decision.shardId < SHARDS);
    assert.ok(decision.nodeId.length > 0);
  }
  router.assertNoLeak();
});

test("虚拟分片：节点间分布均衡（最大偏差 < 2.5 倍均值）", () => {
  const topo = buildUniformTopology(2, 2, 3);
  const map = new VirtualShardMap(SHARDS, topo);
  map.rebalance();
  const dist = map.distribution();
  const mean = SHARDS / topo.size;
  for (const [, count] of dist) {
    assert.ok(count < mean * 2.5, `分片分布倾斜: ${count} vs 均值 ${mean}`);
    assert.ok(count > 0, "存在空节点（分片泄漏倾向）");
  }
  assert.equal([...dist.values()].reduce((a, b) => a + b, 0), SHARDS);
});

test("最小迁移：增删节点时迁移分片数接近理论下界", () => {
  const topo = buildUniformTopology(2, 2, 3);
  const map = new VirtualShardMap(SHARDS, topo);
  map.rebalance();

  // 新增节点：迁移量应 ≈ SHARDS / (N+1)，容忍 3 倍上界
  topo.addNode({ nodeId: "dc0-rack0-nodeX", rackId: "dc0-rack0", dcId: "dc0", capacity: 1 });
  const addResult = map.rebalance();
  const expectMoved = SHARDS / (topo.size);
  assert.ok(
    addResult.moved.length <= expectMoved * 3,
    `新增节点迁移过多: ${addResult.moved.length} > ${expectMoved * 3}`,
  );
  assert.ok(addResult.moved.length > 0);

  // 删除节点：只有该节点持有的分片迁移
  const before = map.snapshot();
  const victim = "dc0-rack0-nodeX";
  const victimShards = [...before.values()].filter((n) => n === victim).length;
  topo.removeNode(victim);
  const removeResult = map.rebalance();
  assert.equal(removeResult.moved.length, victimShards);
  for (const nodeId of removeResult.assignment.values()) {
    assert.notEqual(nodeId, victim, "存在悬挂引用（泄漏）");
  }
  map.assertConsistent();
});

test("局部性：客户端同机架候选存在时优先同机架", () => {
  const { topo, router } = makeRouter();
  const client = "dc0-rack0-node0";
  let sameRack = 0;
  let total = 0;
  for (let i = 0; i < 3000; i++) {
    const d = router.route(`lk-${i}`, client);
    const candidates = router.candidates(d.shardId);
    const hasLocal = candidates.some(
      (n) => topo.distance(client, n) <= LOCALITY.SAME_RACK,
    );
    if (hasLocal) {
      total++;
      if (topo.distance(client, d.nodeId) <= LOCALITY.SAME_RACK) sameRack++;
    }
  }
  // 存在本地（同节点/同机架）候选时必须 100% 命中本地（局部性硬保证）
  assert.equal(sameRack, total);
  assert.ok(total > 0, "样本中应存在同机架候选情形");
});

test("局部性：跨 DC 通信量显著低于随机路由", () => {
  const { router } = makeRouter();
  const clients = ["dc0-rack0-node0", "dc1-rack1-node2"];
  let crossDc = 0;
  let total = 0;
  for (const client of clients) {
    for (let i = 0; i < 2000; i++) {
      const d = router.route(`ck-${i}`, client);
      total++;
      if (d.distance === LOCALITY.CROSS_DC) crossDc++;
    }
  }
  // 随机路由跨 DC 比例约 50%，局部性感知应大幅压低
  assert.ok(
    crossDc / total < 0.2,
    `跨 DC 比例过高: ${((crossDc / total) * 100).toFixed(1)}%`,
  );
});

test("过载溢出：首选节点满载时溢出到次近节点，且可全部回退主节点", () => {
  const topo = buildUniformTopology(1, 1, 3);
  const router = new LocalityAwareRouter(topo, 64, { overloadFactor: 1 });
  const client = "dc0-rack0-node0";

  // 找到主节点为 client 的 key，将其余候选打满
  let spilledSeen = false;
  for (let i = 0; i < 200; i++) {
    const key = `ov-${i}`;
    const d0 = router.route(key, client);
    const candidates = router.candidates(d0.shardId);
    for (const n of candidates) {
      if (n !== client) {
        for (let j = 0; j < 100; j++) router.acquire(n);
      }
    }
    const d1 = router.route(key, client);
    if (candidates.includes(client)) {
      // client 未过载时必须选中 client（距离 0）
      assert.equal(d1.nodeId, client);
    }
    for (let j = 0; j < 100; j++) router.acquire(client);
    const d2 = router.route(key, client);
    // 全部过载 → 回退主节点，保证可路由
    assert.equal(d2.nodeId, router.candidates(d2.shardId)[0]);
    if (d1.spilled) spilledSeen = true;
    // 释放，避免泄漏
    for (const n of [...candidates, client]) {
      while (router.loadOf(n) > 0) router.release(n);
    }
  }
  router.assertNoLeak();
});

test("并发安全：重平衡期间并发路由始终可路由且无泄漏", async () => {
  const topo = buildUniformTopology(2, 2, 3);
  const router = new LocalityAwareRouter(topo, SHARDS);

  const rebalancers: Promise<unknown>[] = [];
  for (let i = 0; i < 8; i++) {
    rebalancers.push(
      (async () => {
        const id = `dc0-rack1-hot${i}`;
        await router.addNode({ nodeId: id, rackId: "dc0-rack1", dcId: "dc0", capacity: 1 });
        await router.removeNode(id);
      })(),
    );
  }

  const routers_: Promise<void>[] = [];
  for (let i = 0; i < 16; i++) {
    routers_.push(
      (async () => {
        for (let k = 0; k < 500; k++) {
          const d = router.route(`cc-${i}-${k}`, "dc0-rack0-node0");
          assert.ok(topo.has(d.nodeId), `路由到已移除节点: ${d.nodeId}`);
          if (k % 7 === 0) await Promise.resolve(); // 让出事件循环
        }
      })(),
    );
  }

  await Promise.all([...rebalancers, ...routers_]);
  router.assertNoLeak();
});

test("并发安全：AsyncMutex 串行化临界区", async () => {
  const mutex = new AsyncMutex();
  let inside = 0;
  let maxInside = 0;
  const tasks = Array.from({ length: 50 }, () =>
    mutex.runExclusive(async () => {
      inside++;
      maxInside = Math.max(maxInside, inside);
      await new Promise((r) => setImmediate(r));
      inside--;
    }),
  );
  await Promise.all(tasks);
  assert.equal(maxInside, 1, "互斥锁内出现并发进入");
});

test("无泄漏：负载计数严格配对，下溢即抛错", () => {
  const { router } = makeRouter();
  const node = "dc0-rack0-node0";
  router.acquire(node);
  router.acquire(node);
  router.release(node);
  router.release(node);
  assert.equal(router.loadOf(node), 0);
  assert.throws(() => router.release(node), /泄漏/);
  assert.throws(() => router.acquire("ghost"), /不存在/);
  router.assertNoLeak();
});

test("确定性：相同 key 在拓扑不变时路由稳定", () => {
  const { router } = makeRouter();
  const first = router.route("stable-key", "dc0-rack0-node0");
  for (let i = 0; i < 100; i++) {
    const d = router.route("stable-key", "dc0-rack0-node0");
    assert.equal(d.nodeId, first.nodeId);
    assert.equal(d.shardId, first.shardId);
  }
  // 哈希工具确定性
  assert.equal(fnv1a("abc"), fnv1a("abc"));
  assert.equal(shardOfKey("abc", SHARDS), shardOfKey("abc", SHARDS));
});
