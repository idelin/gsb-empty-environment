import { test } from "node:test";
import assert from "node:assert/strict";
import {
  LinkStateDatabase,
  VersionConflictError,
  InvalidUpdateError,
} from "../src/lsdb.ts";
import type { EdgeUpdate } from "../src/lsdb.ts";
import { checkConsistency } from "../src/verify.ts";

const add = (from: string, to: string, weight: number, epoch: number, seq: number): EdgeUpdate => ({
  from,
  to,
  weight,
  version: { epoch, seq },
});

const del = (from: string, to: string, weight: number, epoch: number, seq: number): EdgeUpdate => ({
  from,
  to,
  weight,
  version: { epoch, seq },
  deleted: true,
});

test("平行输入去重:同版本同内容忽略,同批次内也去重", () => {
  const db = new LinkStateDatabase();
  const u = add("a", "b", 3, 1, 1);

  let r = db.applyBatch([u]);
  assert.equal(r.applied, 1);

  // 泛洪重复投递(跨批次)
  r = db.applyBatch([u, { ...u }]);
  assert.equal(r.applied, 0);
  assert.equal(r.duplicates, 2);

  // 同批次内:先新后旧、先新后重复
  r = db.applyBatch([add("a", "c", 1, 1, 2), add("a", "c", 1, 1, 2), add("a", "c", 9, 1, 1)]);
  assert.equal(r.applied, 1);
  assert.equal(r.duplicates, 1);
  assert.equal(r.stale, 1);

  assert.equal(db.getEdge("a", "b")!.weight, 3);
  assert.equal(db.getEdge("a", "c")!.weight, 1);
});

test("同版本内容冲突报错(权重或删除标志不同)", () => {
  const db = new LinkStateDatabase();
  db.applyBatch([add("a", "b", 3, 1, 1)]);
  assert.throws(() => db.applyBatch([add("a", "b", 4, 1, 1)]), VersionConflictError);
  assert.throws(() => db.applyBatch([del("a", "b", 3, 1, 1)]), VersionConflictError);
  // 报错后原记录不变
  assert.equal(db.getEdge("a", "b")!.weight, 3);
  assert.equal(db.getEdge("a", "b")!.deleted, false);
});

test("旧删除与删除墓碑:旧消息不能复活已删除的边", () => {
  const db = new LinkStateDatabase();
  db.applyBatch([add("a", "b", 3, 1, 1), add("b", "c", 2, 1, 1)]);

  // 过期删除(版本更旧)被忽略
  let r = db.applyBatch([del("a", "b", 3, 1, 0)].map((u) => ({ ...u, version: { epoch: 0, seq: 9 } })));
  assert.equal(r.stale, 1);
  assert.equal(db.getEdge("a", "b")!.deleted, false);

  // 正常删除,留下墓碑
  r = db.applyBatch([del("a", "b", 3, 1, 2)]);
  assert.equal(r.applied, 1);
  const tomb = db.getEdge("a", "b")!;
  assert.equal(tomb.deleted, true);
  assert.deepEqual(tomb.version, { epoch: 1, seq: 2 });
  assert.deepEqual(db.routeTo("a", "b"), { reachable: false });

  // 延迟到达的旧新增(版本低于墓碑)不能复活
  r = db.applyBatch([add("a", "b", 3, 1, 1)]);
  assert.equal(r.stale, 1);
  assert.equal(db.getEdge("a", "b")!.deleted, true);
  assert.deepEqual(db.routeTo("a", "b"), { reachable: false });

  // 与墓碑同版本但内容不同(非删除)→ 冲突
  assert.throws(() => db.applyBatch([add("a", "b", 3, 1, 2)]), VersionConflictError);

  // 只有更新版本才能复活
  r = db.applyBatch([add("a", "b", 5, 1, 3)]);
  assert.equal(r.applied, 1);
  assert.equal(db.getEdge("a", "b")!.deleted, false);
  const route = db.routeTo("a", "c");
  assert.ok(route.reachable);
  assert.equal(route.route.distance, 7);
});

test("节点重启升 epoch:新 epoch 的小序号覆盖旧 epoch 的大序号", () => {
  const db = new LinkStateDatabase();
  db.applyBatch([add("n1", "n2", 5, 1, 9)]);

  // n1 重启,epoch 升到 2,序号重新从 1 开始
  let r = db.applyBatch([add("n1", "n2", 7, 2, 1)]);
  assert.equal(r.applied, 1);
  assert.equal(db.getEdge("n1", "n2")!.weight, 7);

  // 重启前发出的旧通告(1, 10)延迟到达:序号虽大但 epoch 旧 → 过期
  r = db.applyBatch([add("n1", "n2", 1, 1, 10)]);
  assert.equal(r.stale, 1);
  assert.equal(db.getEdge("n1", "n2")!.weight, 7);
});

test("零权环:查询终止且结果正确", () => {
  const db = new LinkStateDatabase();
  db.applyBatch([
    add("s", "a", 0, 1, 1),
    add("a", "b", 0, 1, 1),
    add("b", "a", 0, 1, 1), // 零权环 a <-> b
    add("b", "t", 3, 1, 1),
    add("t", "a", 0, 1, 1), // 反向零权边,更多环
  ]);

  const route = db.routeTo("s", "t");
  assert.ok(route.reachable);
  assert.equal(route.route.distance, 3);
  assert.equal(route.route.hops, 3);
  assert.deepEqual(route.route.path, ["s", "a", "b", "t"]);

  // 纯零权环孤岛:不可达必须单独返回,且查询终止
  const db2 = new LinkStateDatabase();
  db2.applyBatch([add("x", "y", 0, 1, 1), add("y", "x", 0, 1, 1)]);
  assert.deepEqual(db2.routeTo("x", "nowhere"), { reachable: false });
});

test("等距路径:先比跳数,再比完整节点 ID 序列字典序", () => {
  // 距离相同,跳数少者胜
  const db = new LinkStateDatabase();
  db.applyBatch([
    add("s", "t", 5, 1, 1),
    add("s", "a", 2, 1, 2),
    add("a", "t", 3, 1, 1),
  ]);
  let route = db.routeTo("s", "t");
  assert.ok(route.reachable);
  assert.equal(route.route.distance, 5);
  assert.equal(route.route.hops, 1);
  assert.equal(route.route.nextHop, "t");

  // 距离与跳数都相同,字典序小者胜(比较完整序列)
  const db2 = new LinkStateDatabase();
  db2.applyBatch([
    add("s", "b", 1, 1, 1),
    add("b", "t", 1, 1, 1),
    add("s", "a", 1, 1, 2),
    add("a", "t", 1, 1, 1),
  ]);
  route = db2.routeTo("s", "t");
  assert.ok(route.reachable);
  assert.deepEqual(route.route.path, ["s", "a", "t"]);
  assert.equal(route.route.nextHop, "a");

  // 公共前缀之后的节点决定字典序:[s,a,b,t] < [s,a,c,t]
  const db3 = new LinkStateDatabase();
  db3.applyBatch([
    add("s", "a", 0, 1, 1),
    add("a", "c", 1, 1, 1),
    add("c", "t", 1, 1, 1),
    add("a", "b", 1, 1, 2),
    add("b", "t", 1, 1, 1),
  ]);
  route = db3.routeTo("s", "t");
  assert.ok(route.reachable);
  assert.deepEqual(route.route.path, ["s", "a", "b", "t"]);
});

test("批次原子性:冲突导致整批回滚,无部分应用", () => {
  const db = new LinkStateDatabase();
  db.applyBatch([add("x", "y", 1, 1, 1)]);
  const before = db.snapshotEdges();

  assert.throws(
    () =>
      db.applyBatch([
        add("p", "q", 1, 1, 1), // 本可成功应用
        add("x", "y", 9, 1, 1), // 与现有记录同版本冲突
        del("p", "q", 1, 1, 2),
      ]),
    VersionConflictError,
  );

  assert.equal(db.getEdge("p", "q"), undefined, "批次中的合法更新也不得应用");
  assert.deepEqual(db.snapshotEdges(), before, "LSDB 必须保持批次前状态");

  // 非法输入同样在应用任何变更前拒绝
  assert.throws(
    () => db.applyBatch([add("m", "n", 1, 1, 1), add("m", "n", -2, 1, 2)]),
    InvalidUpdateError,
  );
  assert.equal(db.getEdge("m", "n"), undefined);
});

test("不可达单独返回,不用任意大数冒充无穷", () => {
  const db = new LinkStateDatabase();
  db.applyBatch([add("a", "b", 1, 1, 1)]);

  // 反向不可达、不存在的节点、与源完全不连通的节点
  assert.deepEqual(db.routeTo("b", "a"), { reachable: false });
  assert.deepEqual(db.routeTo("a", "zzz"), { reachable: false });
  assert.deepEqual(db.routeTo("lonely", "a"), { reachable: false });

  const routes = db.routesFrom("a");
  assert.equal(routes.has("zzz"), false);
  for (const r of routes.values()) {
    assert.ok(Number.isInteger(r.distance) && r.distance >= 0);
    assert.ok(r.distance < 1e9, "距离必须是真实值而非无穷占位");
  }
});

test("缓存失效覆盖被删边影响的路径,也覆盖新增更优边", () => {
  const db = new LinkStateDatabase();
  db.applyBatch([
    add("s", "a", 1, 1, 1),
    add("a", "t", 1, 1, 1),
    add("s", "b", 1, 1, 2),
    add("b", "t", 5, 1, 1),
  ]);

  let route = db.routeTo("s", "t");
  assert.ok(route.reachable);
  assert.equal(route.route.distance, 2);
  assert.deepEqual(route.route.path, ["s", "a", "t"]);
  assert.equal(db.cachedSourceCount, 1);

  // 删除路径上的边 → 缓存必须失效,改走 b
  db.applyBatch([del("a", "t", 1, 1, 2)]);
  assert.equal(db.cachedSourceCount, 0, "被删边影响到的缓存必须失效");
  route = db.routeTo("s", "t");
  assert.ok(route.reachable);
  assert.equal(route.route.distance, 6);
  assert.deepEqual(route.route.path, ["s", "b", "t"]);

  // 再删除 b -> t → 不可达
  db.applyBatch([del("b", "t", 5, 1, 2)]);
  assert.deepEqual(db.routeTo("s", "t"), { reachable: false });

  // 新增一条更优的边(此前不存在)→ 缓存同样失效
  db.routeTo("s", "t"); // 重新建立缓存
  assert.equal(db.cachedSourceCount, 1);
  db.applyBatch([add("s", "t", 1, 1, 3)]);
  assert.equal(db.cachedSourceCount, 0);
  route = db.routeTo("s", "t");
  assert.ok(route.reachable);
  assert.equal(route.route.distance, 1);
  assert.equal(route.route.nextHop, "t");
});

test("路由输出版本:路径最后一条边的版本", () => {
  const db = new LinkStateDatabase();
  db.applyBatch([
    add("s", "a", 1, 3, 7),
    add("a", "t", 2, 5, 4),
  ]);
  const route = db.routeTo("s", "t");
  assert.ok(route.reachable);
  assert.deepEqual(route.route.version, { epoch: 5, seq: 4 });
  const direct = db.routeTo("s", "a");
  assert.ok(direct.reachable);
  assert.deepEqual(direct.route.version, { epoch: 3, seq: 7 });
});

test("随机小图交叉核对:Bellman-Ford 核对距离,简单路径穷举核对决胜顺序", () => {
  // 可复现的 LCG 随机数
  const lcg = (seed: number) => {
    let s = seed >>> 0;
    return () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  };

  const NODE_COUNT = 5;
  const nodes = Array.from({ length: NODE_COUNT }, (_, i) => `n${i}`);

  for (let seed = 1; seed <= 40; seed++) {
    const rand = lcg(seed);
    const db = new LinkStateDatabase();
    const seqByFrom = new Map<string, number>();
    const nextVersion = (from: string) => ({
      epoch: 1,
      seq: (seqByFrom.get(from) ?? 0) + 1,
    });

    // 随机批次:含零权边、自环、删除,模拟乱序到达
    let remaining = 24;
    while (remaining-- > 0) {
      const batch: EdgeUpdate[] = [];
      const batchSize = 1 + Math.floor(rand() * 4);
      for (let i = 0; i < batchSize; i++) {
        const from = nodes[Math.floor(rand() * NODE_COUNT)];
        const to = rand() < 0.1 ? from : nodes[Math.floor(rand() * NODE_COUNT)];
        const version = nextVersion(from);
        seqByFrom.set(from, version.seq);
        batch.push({
          from,
          to,
          weight: Math.floor(rand() * 5), // 含 0 权
          version,
          deleted: rand() < 0.15,
        });
      }
      db.applyBatch(batch);
    }

    checkConsistency(db, nodes);
  }
});
