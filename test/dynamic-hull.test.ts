import { test } from "node:test";
import assert from "node:assert/strict";
import { DynamicConvexHull, type TimedPoint } from "../src/dynamic-hull.ts";
import { cross, signedArea, type Vec } from "../src/geometry.ts";

/** 可复现的伪随机数发生器 */
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

/** 参考实现：全量单调链（Graham 系）凸包，逆时针、无共线冗余点 */
function bruteForceHull(points: Vec[]): Vec[] {
  const uniq = new Map<string, Vec>();
  for (const p of points) uniq.set(`${p.x},${p.y}`, p);
  const pts = [...uniq.values()].sort((a, b) => a.x - b.x || a.y - b.y);
  if (pts.length <= 1) return pts;
  const lower: Vec[] = [];
  for (const p of pts) {
    while (lower.length >= 2 && cross(lower[lower.length - 2], lower[lower.length - 1], p) <= 0) {
      lower.pop();
    }
    lower.push(p);
  }
  const upper: Vec[] = [];
  for (let i = pts.length - 1; i >= 0; i--) {
    const p = pts[i];
    while (upper.length >= 2 && cross(upper[upper.length - 2], upper[upper.length - 1], p) <= 0) {
      upper.pop();
    }
    upper.push(p);
  }
  lower.pop();
  upper.pop();
  return [...lower, ...upper];
}

/** 两个逆时针凸包序列规范化（旋转到字典序最小顶点起始）后比较 */
function canonical(hull: Vec[]): string {
  if (hull.length === 0) return "";
  const keys = hull.map((p) => `${p.x},${p.y}`);
  let best = 0;
  for (let i = 1; i < keys.length; i++) {
    if (keys[i] < keys[best]) best = i;
  }
  return [...keys.slice(best), ...keys.slice(0, best)].join("|");
}

function assertHullMatches(actual: TimedPoint[], expected: Vec[], context: string): void {
  assert.equal(
    canonical(actual),
    canonical(expected),
    `${context}\nactual:   ${JSON.stringify(actual.map((p) => [p.x, p.y]))}\nexpected: ${JSON.stringify(expected.map((p) => [p.x, p.y]))}`,
  );
  if (actual.length >= 3) {
    assert.ok(signedArea(actual) > 0, `${context}: hull must be counter-clockwise`);
  }
}

test("空集与退化情形", () => {
  const hull = new DynamicConvexHull();
  assert.deepEqual(hull.getHull(), []);
  assert.equal(hull.aliveCount, 0);

  hull.add(3, 4, 10);
  assertHullMatches(hull.getHull(), [{ x: 3, y: 4 }], "single point");

  hull.add(3, 4, 10); // 重复坐标
  assertHullMatches(hull.getHull(), [{ x: 3, y: 4 }], "duplicate point");

  hull.add(8, 4, 10);
  assertHullMatches(hull.getHull(), [{ x: 3, y: 4 }, { x: 8, y: 4 }], "two points");
});

test("静态点集与全量扫描一致", () => {
  const rng = mulberry32(42);
  const hull = new DynamicConvexHull();
  const pts: Vec[] = [];
  for (let i = 0; i < 200; i++) {
    const p = { x: Math.floor(rng() * 50), y: Math.floor(rng() * 50) };
    pts.push(p);
    hull.add(p.x, p.y, 1000);
  }
  assertHullMatches(hull.getHull(), bruteForceHull(pts), "static 200 points");
});

test("内部点插入不改变凸包，且无需链扫描", () => {
  const hull = new DynamicConvexHull();
  hull.add(0, 0, 100);
  hull.add(10, 0, 100);
  hull.add(10, 10, 100);
  hull.add(0, 10, 100);
  const sweepsBefore = hull.getStats().insertSweeps;
  hull.add(5, 5, 100);
  hull.add(3, 7, 100);
  assertHullMatches(
    hull.getHull(),
    [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 10 }, { x: 0, y: 10 }],
    "square with interior points",
  );
  assert.equal(hull.getStats().insertSweeps, sweepsBefore, "interior inserts must not touch chains");
});

test("TTL 过期使老点自然淡出", () => {
  const hull = new DynamicConvexHull();
  hull.add(0, 0, 2);
  hull.add(10, 0, 2);
  hull.add(5, 8, 2);
  assert.equal(hull.getHull().length, 3);

  hull.advanceTo(1);
  assert.equal(hull.aliveCount, 3);

  const after = hull.advanceTo(2); // 到达失效时刻，全部过期
  assert.equal(after.length, 0);
  assert.equal(hull.aliveCount, 0);
  assert.equal(hull.getStats().expiredTotal, 3);
});

test("过期点为非顶点时零几何代价", () => {
  const hull = new DynamicConvexHull();
  hull.add(0, 0, 100);
  hull.add(10, 0, 100);
  hull.add(10, 10, 100);
  hull.add(0, 10, 100);
  hull.add(5, 5, 1); // 内部点，短 TTL
  const repairsBefore = hull.getStats().localRepairs;
  hull.advanceTo(1);
  assertHullMatches(
    hull.getHull(),
    [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 10 }, { x: 0, y: 10 }],
    "interior expiry keeps hull",
  );
  assert.equal(hull.getStats().localRepairs, repairsBefore, "interior expiry must not repair chains");
});

test("凸包顶点过期触发局部修复并暴露新顶点", () => {
  const hull = new DynamicConvexHull();
  hull.add(0, 0, 100);
  hull.add(10, 0, 100);
  hull.add(10, 10, 100);
  hull.add(0, 10, 100);
  hull.add(5, 12, 1); // 顶部尖点，1 时隙后过期
  hull.add(5, 10.5, 100); // 被尖点遮挡，尖点过期后应暴露为顶点
  assert.equal(hull.getHull().length, 5);

  const after = hull.advanceTo(1);
  assertHullMatches(
    after,
    [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 10 }, { x: 5, y: 10.5 }, { x: 0, y: 10 }],
    "apex expired, hidden point exposed",
  );
  assert.ok(hull.getStats().localRepairs > 0, "vertex expiry must trigger local repair");
  assert.equal(hull.getStats().fullRescans, 0);
});

test("共线点与竖列处理", () => {
  const hull = new DynamicConvexHull();
  hull.add(0, 0, 100);
  hull.add(5, 0, 100); // 底边共线中点
  hull.add(10, 0, 100);
  hull.add(10, 5, 100); // 右边共线中点
  hull.add(10, 10, 100);
  hull.add(0, 10, 100);
  assertHullMatches(
    hull.getHull(),
    [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 10 }, { x: 0, y: 10 }],
    "collinear edge points stripped",
  );

  const col = new DynamicConvexHull();
  col.add(4, 1, 100);
  col.add(4, 9, 100);
  col.add(4, 5, 100);
  col.add(0, 5, 100);
  col.add(8, 5, 100);
  assertHullMatches(
    col.getHull(),
    [{ x: 0, y: 5 }, { x: 4, y: 1 }, { x: 8, y: 5 }, { x: 4, y: 9 }],
    "vertical column extremes",
  );
});

test("未来点预约进入", () => {
  const hull = new DynamicConvexHull();
  hull.add(0, 0, 100);
  hull.add(10, 0, 100);
  hull.schedule(5, 10, 100, 5); // t=5 才进入
  assert.equal(hull.getHull().length, 2);

  hull.advanceTo(4);
  assert.equal(hull.getHull().length, 2);

  const after = hull.advanceTo(5);
  assertHullMatches(after, [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 5, y: 10 }], "scheduled point enters");
});

test("参数校验", () => {
  const hull = new DynamicConvexHull();
  assert.throws(() => hull.add(0, 0, 0), /ttl/);
  assert.throws(() => hull.add(0, 0, -3), /ttl/);
  assert.throws(() => hull.add(NaN, 0, 1), /finite/);
  hull.add(1, 1, 10);
  hull.advanceTo(5);
  assert.throws(() => hull.advanceTo(4), /backwards/);
  assert.throws(() => hull.schedule(0, 0, 1, 2), /earlier/);
});

test("随机动态模拟：逐时隙与全量重算对照", () => {
  const rng = mulberry32(20261002);
  const hull = new DynamicConvexHull();
  const alive = new Map<number, { p: Vec; expiresAt: number }>();

  for (let t = 0; t <= 300; t++) {
    // 新点进入（含部分预约未来进入的点）
    const arrivals = Math.floor(rng() * 5);
    for (let k = 0; k < arrivals; k++) {
      const p = { x: Math.floor(rng() * 60), y: Math.floor(rng() * 60) };
      const ttl = 1 + Math.floor(rng() * 15);
      const enterAt = t + (rng() < 0.3 ? Math.floor(rng() * 4) : 0);
      const id = hull.schedule(p.x, p.y, ttl, enterAt);
      alive.set(id, { p, expiresAt: enterAt + ttl });
    }
    hull.advanceTo(t);

    const expectedAlive: Vec[] = [];
    for (const [id, rec] of alive) {
      if (rec.expiresAt <= t) alive.delete(id);
      else if (hull.getAlivePoints().some((q) => q.id === id)) expectedAlive.push(rec.p);
    }
    assertHullMatches(hull.getHull(), bruteForceHull(expectedAlive), `slot t=${t}`);
  }

  const stats = hull.getStats();
  assert.equal(stats.fullRescans, 0, "never re-runs a full scan");
  assert.ok(stats.insertedTotal > 0 && stats.expiredTotal > 0);
  // 增量性质：大量插入/过期不触碰链
  assert.ok(stats.insertSweeps < stats.insertedTotal, "most inserts are interior");
  assert.ok(stats.localRepairs < stats.expiredTotal, "most expiries are non-vertices");
});

test("批量插入保持正确且增量占优", () => {
  const rng = mulberry32(7);
  const hull = new DynamicConvexHull();
  const pts: Vec[] = [];
  for (let i = 0; i < 5000; i++) {
    const p = { x: rng() * 1000, y: rng() * 1000 };
    pts.push(p);
    hull.add(p.x, p.y, 1e9);
  }
  assertHullMatches(hull.getHull(), bruteForceHull(pts), "5000 random inserts");
  const stats = hull.getStats();
  assert.ok(
    stats.insertSweeps < stats.insertedTotal / 10,
    `only ${stats.insertSweeps}/${stats.insertedTotal} inserts touched the chains`,
  );
});
