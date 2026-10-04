// 独立校验器:不复用 lsdb.ts 的任何路由逻辑,用于交叉核对。
//   - bellmanFord:逐对 (dist, hops) 松弛,核对距离与跳数;
//   - bestSimplePath:穷举所有简单路径,核对 (距离, 跳数, 字典序) 的完整决胜顺序;
//   - checkConsistency:对给定节点集合全源全目的比对 LSDB 的查询结果。

import assert from "node:assert/strict";
import { LinkStateDatabase } from "./lsdb.ts";
import type { EdgeRecord } from "./lsdb.ts";

export interface DistHops {
  dist: number;
  hops: number;
}

/** Bellman-Ford:按 (dist, hops) 字典序松弛 nodes.length - 1 轮。 */
export function bellmanFord(
  nodes: string[],
  edges: EdgeRecord[],
  source: string,
): Map<string, DistHops> {
  const live = edges.filter((e) => !e.deleted);
  const dist = new Map<string, DistHops>();
  dist.set(source, { dist: 0, hops: 0 });
  for (let round = 0; round < nodes.length - 1; round++) {
    let changed = false;
    for (const e of live) {
      const du = dist.get(e.from);
      if (!du) continue;
      const cand: DistHops = { dist: du.dist + e.weight, hops: du.hops + 1 };
      const cur = dist.get(e.to);
      if (!cur || cand.dist < cur.dist || (cand.dist === cur.dist && cand.hops < cur.hops)) {
        dist.set(e.to, cand);
        changed = true;
      }
    }
    if (!changed) break;
  }
  return dist;
}

export interface BestPath {
  dist: number;
  hops: number;
  path: string[];
}

/** 独立的决胜比较器:距离 → 跳数 → 节点 ID 序列字典序。 */
function betterPath(a: BestPath, b: BestPath): boolean {
  if (a.dist !== b.dist) return a.dist < b.dist;
  if (a.hops !== b.hops) return a.hops < b.hops;
  for (let i = 0; i < Math.min(a.path.length, b.path.length); i++) {
    if (a.path[i] !== b.path[i]) return a.path[i] < b.path[i];
  }
  return a.path.length < b.path.length;
}

/** 穷举 source → dest 的所有简单路径,返回决胜最优;不可达返回 null。 */
export function bestSimplePath(
  edges: EdgeRecord[],
  source: string,
  dest: string,
): BestPath | null {
  const adj = new Map<string, { to: string; weight: number }[]>();
  for (const e of edges) {
    if (e.deleted) continue;
    let list = adj.get(e.from);
    if (!list) adj.set(e.from, (list = []));
    list.push({ to: e.to, weight: e.weight });
  }

  let best: BestPath | null = null;
  const visited = new Set<string>([source]);
  const path: string[] = [source];

  const dfs = (u: string, dist: number): void => {
    if (u === dest) {
      const cand: BestPath = { dist, hops: path.length - 1, path: [...path] };
      if (best === null || betterPath(cand, best)) best = cand;
      return; // 简单路径在首次到达 dest 时结束,继续延伸必然成环
    }
    for (const e of adj.get(u) ?? []) {
      if (visited.has(e.to)) continue;
      visited.add(e.to);
      path.push(e.to);
      dfs(e.to, dist + e.weight);
      path.pop();
      visited.delete(e.to);
    }
  };
  dfs(source, 0);
  return best;
}

/**
 * 对 nodes 中每个源节点核对 LSDB 的路由表:
 * 距离/跳数对 Bellman-Ford,完整路径与决胜顺序对简单路径穷举,
 * 并核对下一跳、版本与不可达表达。先清缓存,保证核对的是真实重算结果。
 */
export function checkConsistency(db: LinkStateDatabase, nodes: string[]): void {
  db.clearCache();
  const edges = db.snapshotEdges();

  for (const source of nodes) {
    const routes = db.routesFrom(source);
    const bf = bellmanFord(nodes, edges, source);

    // 源节点自身
    const self = routes.get(source);
    assert.ok(self, `${source}: missing self route`);
    assert.equal(self.distance, 0, `${source}: self distance`);
    assert.equal(self.hops, 0, `${source}: self hops`);
    assert.equal(self.nextHop, null, `${source}: self nextHop`);
    assert.deepEqual(self.path, [source], `${source}: self path`);
    assert.equal(self.version, null, `${source}: self version`);

    for (const dest of nodes) {
      if (dest === source) continue;
      const want = bestSimplePath(edges, source, dest);
      const got = routes.get(dest);
      const single = db.routeTo(source, dest);

      if (want === null) {
        assert.equal(got, undefined, `${source} -> ${dest}: should be unreachable`);
        assert.deepEqual(single, { reachable: false }, `${source} -> ${dest}: reachable flag`);
        assert.equal(bf.has(dest), false, `${source} -> ${dest}: BF should be unreachable`);
        continue;
      }

      assert.ok(got, `${source} -> ${dest}: missing route`);
      assert.ok(single.reachable, `${source} -> ${dest}: reachable flag`);

      // 距离与跳数:Bellman-Ford 与简单路径穷举一致
      const bfDest = bf.get(dest);
      assert.ok(bfDest, `${source} -> ${dest}: BF missing`);
      assert.equal(bfDest.dist, want.dist, `${source} -> ${dest}: BF dist vs enumeration`);
      assert.equal(bfDest.hops, want.hops, `${source} -> ${dest}: BF hops vs enumeration`);

      // LSDB 查询结果与两者一致
      assert.equal(got.distance, want.dist, `${source} -> ${dest}: distance`);
      assert.equal(got.hops, want.hops, `${source} -> ${dest}: hops`);
      assert.deepEqual(got.path, want.path, `${source} -> ${dest}: path (tie-break order)`);
      assert.equal(got.nextHop, want.path[1], `${source} -> ${dest}: nextHop`);

      // 版本 = 路径最后一条边的版本
      const lastEdge = db.getEdge(want.path[want.path.length - 2], dest);
      assert.ok(lastEdge, `${source} -> ${dest}: last edge record`);
      assert.deepEqual(got.version, lastEdge.version, `${source} -> ${dest}: version`);
    }

    // 路由表不应包含 nodes 之外或不可达的目的
    for (const dest of routes.keys()) {
      assert.ok(nodes.includes(dest), `${source}: unexpected destination ${dest}`);
    }
  }
}
