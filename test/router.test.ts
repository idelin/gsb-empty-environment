import test from "node:test";
import assert from "node:assert/strict";
import { Router } from "../src/router.ts";
import { ConflictError, InvalidUpdateError } from "../src/lsdb.ts";
import { computeRoutes } from "../src/spf.ts";
import { bellmanFordDistances, exhaustiveBestPaths } from "../src/verify.ts";
import type { EdgeUpdate } from "../src/lsdb.ts";

const add = (src: string, dst: string, weight: number, epoch: number, seq: number): EdgeUpdate => ({
  src,
  dst,
  weight,
  version: { epoch, seq },
});
const del = (src: string, dst: string, epoch: number, seq: number): EdgeUpdate => ({
  src,
  dst,
  weight: null,
  version: { epoch, seq },
});

test("平行输入去重：批次内与跨批次的重复更新被幂等忽略", () => {
  const r = new Router();
  const res1 = r.applyBatch([add("A", "B", 5, 1, 1), add("A", "B", 5, 1, 1)]);
  assert.equal(res1.applied, 1);
  assert.equal(res1.ignored, 1);

  // 同样的批次再发一遍（并行通告源重复投递）
  const res2 = r.applyBatch([add("A", "B", 5, 1, 1), add("A", "B", 5, 1, 1)]);
  assert.equal(res2.applied, 0);
  assert.equal(res2.ignored, 2);

  const route = r.routeTo("A", "B");
  assert.deepEqual(route.reachable && route.distance, 5);
});

test("重复版本内容冲突：同版本不同权重、新增与删除对撞均报错", () => {
  const r = new Router();
  r.applyBatch([add("A", "B", 5, 1, 1)]);
  assert.throws(() => r.applyBatch([add("A", "B", 9, 1, 1)]), ConflictError);

  r.applyBatch([del("A", "B", 1, 2)]);
  assert.throws(() => r.applyBatch([add("A", "B", 5, 1, 2)]), ConflictError);
});

test("旧删除与旧新增：墓碑不被旧消息复活，旧删除压不住新新增", () => {
  const r = new Router();
  r.applyBatch([add("A", "B", 5, 1, 5)]);
  r.applyBatch([del("A", "B", 1, 6)]); // 删除，留下 (1,6) 墓碑
  assert.equal(r.routeTo("A", "B").reachable, false);

  // 延迟到达的旧新增（版本低于墓碑）被忽略，边保持删除
  const res1 = r.applyBatch([add("A", "B", 5, 1, 5)]);
  assert.equal(res1.ignored, 1);
  assert.equal(r.routeTo("A", "B").reachable, false);

  // 延迟到达的旧删除（版本低于当前新增）同样被忽略
  r.applyBatch([add("A", "B", 7, 1, 7)]);
  const res2 = r.applyBatch([del("A", "B", 1, 6)]);
  assert.equal(res2.ignored, 1);
  const route = r.routeTo("A", "B");
  assert.equal(route.reachable && route.distance, 7);
});

test("节点重启升 epoch：高 epoch 低序号胜过低 epoch 高序号", () => {
  const r = new Router();
  r.applyBatch([add("A", "B", 5, 1, 100)]);
  const res = r.applyBatch([add("A", "B", 9, 2, 0)]); // 重启后序号归零
  assert.equal(res.applied, 1);
  const route = r.routeTo("A", "B");
  assert.equal(route.reachable && route.distance, 9);

  // 重启前的旧通告（低 epoch，序号再大）一律忽略
  const stale = r.applyBatch([add("A", "B", 1, 1, 1_000_000)]);
  assert.equal(stale.ignored, 1);
  const after = r.routeTo("A", "B");
  assert.ok(after.reachable);
  assert.equal(after.distance, 9);
});

test("零权环：算法终止且结果正确", () => {
  const r = new Router();
  r.applyBatch([
    add("A", "B", 0, 1, 1),
    add("B", "A", 0, 1, 1),
    add("B", "C", 0, 1, 2),
    add("C", "A", 0, 1, 3),
    add("A", "C", 5, 1, 4),
  ]);
  const toC = r.routeTo("A", "C");
  assert.ok(toC.reachable);
  assert.equal(toC.distance, 0); // A->B->C 全零权
  assert.equal(toC.hops, 2);
  assert.equal(toC.nextHop, "B");

  // 纯零权环、无出口也能终止
  const r2 = new Router();
  r2.applyBatch([add("X", "Y", 0, 1, 1), add("Y", "X", 0, 1, 1)]);
  const toY = r2.routeTo("X", "Y");
  assert.ok(toY.reachable);
  assert.equal(toY.distance, 0);
});

test("等距路径决胜：先比跳数，再比完整节点 ID 序列字典序", () => {
  const r = new Router();
  r.applyBatch([
    add("A", "D", 2, 1, 1),
    add("A", "B", 1, 1, 2),
    add("B", "D", 1, 1, 3),
    add("A", "C", 1, 1, 4),
    add("C", "D", 1, 1, 5),
  ]);
  // 距离同为 2：直达 1 跳胜出
  let toD = r.routeTo("A", "D");
  assert.ok(toD.reachable);
  assert.equal(toD.distance, 2);
  assert.equal(toD.hops, 1);
  assert.equal(toD.nextHop, "D");

  // 删掉直达边后，[A,B,D] 与 [A,C,D] 等距等跳，字典序选 B
  r.applyBatch([del("A", "D", 1, 6)]);
  toD = r.routeTo("A", "D");
  assert.ok(toD.reachable);
  assert.equal(toD.distance, 2);
  assert.equal(toD.hops, 2);
  assert.deepEqual(toD.path, ["A", "B", "D"]);
  assert.equal(toD.nextHop, "B");
});

test("批次回滚：冲突条目导致整批不生效，缓存不受影响", () => {
  const r = new Router();
  r.applyBatch([add("A", "B", 5, 1, 1)]);
  const before = r.routeTo("A", "B");
  const cachedBefore = r.cachedSourceCount;

  assert.throws(
    () => r.applyBatch([add("A", "C", 3, 1, 2), add("A", "B", 9, 1, 1)]),
    ConflictError,
  );
  // 有效条目 A->C 也不可见
  assert.equal(r.lsdb.get("A", "C"), null);
  assert.equal(r.lsdb.get("A", "B")!.weight, 5);
  assert.equal(r.routeTo("A", "C").reachable, false);
  assert.deepEqual(r.routeTo("A", "B"), before);
  assert.equal(r.cachedSourceCount, cachedBefore);

  // 非法权重同样整批回滚
  assert.throws(
    () => r.applyBatch([add("A", "C", 3, 1, 2), add("A", "D", -1, 1, 3)]),
    InvalidUpdateError,
  );
  assert.equal(r.lsdb.get("A", "C"), null);
});

test("不可达单独返回，不用大数冒充无穷", () => {
  const r = new Router();
  r.applyBatch([add("A", "B", 1, 1, 1), add("C", "D", 1, 1, 1)]);
  const route = r.routeTo("A", "C");
  assert.equal(route.reachable, false);
  assert.ok(!("distance" in route));

  const all = r.routesFrom("A");
  assert.equal(all.get("C")!.reachable, false);
  assert.equal(all.get("D")!.reachable, false);
  assert.ok(all.get("A")!.reachable);
});

test("路由输出包含距离、下一跳与首边通告版本", () => {
  const r = new Router();
  r.applyBatch([add("A", "B", 4, 3, 7), add("B", "C", 2, 1, 1)]);
  const toC = r.routeTo("A", "C");
  assert.ok(toC.reachable);
  assert.equal(toC.distance, 6);
  assert.equal(toC.nextHop, "B");
  assert.deepEqual(toC.firstEdgeVersion, { epoch: 3, seq: 7 });

  const toSelf = r.routeTo("A", "A");
  assert.ok(toSelf.reachable);
  assert.equal(toSelf.distance, 0);
  assert.equal(toSelf.nextHop, null);
  assert.equal(toSelf.firstEdgeVersion, null);
});

test("缓存失效：覆盖被删边影响的路径，无关源的缓存保留", () => {
  const r = new Router();
  r.applyBatch([
    add("A", "B", 1, 1, 1),
    add("B", "D", 1, 1, 2),
    add("A", "C", 1, 1, 3),
    add("C", "D", 1, 1, 4),
    add("X", "Y", 1, 1, 5), // 独立分量
  ]);
  r.routesFrom("A");
  r.routesFrom("B");
  r.routesFrom("X");
  assert.equal(r.cachedSourceCount, 3);

  // 删除 B->D：A（路径 A->B->D）与 B（路径 B->D）失效，X 保留
  r.applyBatch([del("B", "D", 1, 6)]);
  assert.equal(r.cachedSourceCount, 1);

  const toD = r.routeTo("A", "D");
  assert.ok(toD.reachable);
  assert.deepEqual(toD.path, ["A", "C", "D"]); // 不是旧的缓存结果

  // 删成不可达也要正确反映
  r.applyBatch([del("C", "D", 1, 7), del("A", "C", 1, 8), del("A", "B", 1, 9)]);
  assert.equal(r.routeTo("A", "D").reachable, false);
});

// ---------- 随机小图交叉核对 ----------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("随机小图：Dijkstra 对照 Bellman-Ford 距离与穷举简单路径决胜", () => {
  const rand = mulberry32(20261004);
  for (let iter = 0; iter < 60; iter++) {
    const n = 2 + Math.floor(rand() * 6); // 2..7 个节点
    const names = Array.from({ length: n }, (_, i) => `N${i}`);
    const router = new Router();
    // 每个源节点独立维护 (epoch, seq)，模拟通告方
    const clock = new Map<string, { epoch: number; seq: number }>();
    const history: EdgeUpdate[] = [];
    const tick = (src: string): { epoch: number; seq: number } => {
      let c = clock.get(src);
      if (!c) clock.set(src, (c = { epoch: 1, seq: 0 }));
      if (rand() < 0.08) {
        c.epoch++; // 模拟重启
        c.seq = 0;
      }
      c.seq++;
      return { ...c };
    };

    for (let op = 0; op < 40; op++) {
      const roll = rand();
      if (roll < 0.15 && history.length > 0) {
        // 重放一条历史通告：要么重复去重，要么因过旧被忽略，绝不冲突
        const res = router.applyBatch([history[Math.floor(rand() * history.length)]]);
        assert.equal(res.applied, 0);
        continue;
      }
      const src = names[Math.floor(rand() * n)];
      let dst = names[Math.floor(rand() * n)];
      if (dst === src) dst = names[(names.indexOf(src) + 1) % n];
      const v = tick(src);
      const u: EdgeUpdate =
        roll < 0.45
          ? { src, dst, weight: null, version: v } // 删除
          : { src, dst, weight: Math.floor(rand() * 7), version: v }; // 含零权
      history.push(u);
      router.applyBatch([u]);
    }

    const adj = router.lsdb.adjacency();
    const source = names[Math.floor(rand() * n)];
    const routes = computeRoutes(adj, router.lsdb.nodes(), source);
    const bfDist = bellmanFordDistances(adj, source);
    const exhaustive = exhaustiveBestPaths(adj, source);

    for (const node of router.lsdb.nodes()) {
      const route = routes.get(node)!;
      const bf = bfDist.get(node);
      const ex = exhaustive.get(node);
      if (bf === undefined) {
        assert.equal(route.reachable, false, `${node} 应不可达 (iter ${iter})`);
        assert.equal(ex, undefined);
        continue;
      }
      assert.ok(route.reachable, `${node} 应可达 (iter ${iter})`);
      assert.equal(route.distance, bf, `${node} 距离与 Bellman-Ford 不符 (iter ${iter})`);
      assert.ok(ex, `${node} 穷举应可达 (iter ${iter})`);
      assert.equal(route.distance, ex.dist, `${node} 距离与穷举不符 (iter ${iter})`);
      assert.equal(route.hops, ex.hops, `${node} 跳数与穷举不符 (iter ${iter})`);
      assert.deepEqual(route.path, ex.path, `${node} 决胜路径与穷举不符 (iter ${iter})`);
      if (route.path.length > 1) assert.equal(route.nextHop, route.path[1]);
    }
  }
});
