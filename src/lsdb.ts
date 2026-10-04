// 本地链路状态数据库(LSDB)+ 最短路查询。
//
// 版本模型:每条有向边的每次更新都携带源节点的 (epoch, seq) 二元组,按字典序比较新旧:
//   - 更新版本 > 本地版本:接受(删除也会留下带版本的墓碑,旧版本的新增无法复活它);
//   - 更新版本 = 本地版本:内容相同 → 忽略(重复投递去重);内容不同 → 抛 VersionConflictError;
//   - 更新版本 < 本地版本:忽略(过期通告)。
// applyBatch 两阶段提交:先在影子副本上顺序校验并应用,任何冲突都会让整批被拒绝,
// LSDB 保持原样(批次原子可见)。
//
// 路由选择:先按距离,再按跳数,最后按完整节点 ID 序列的字典序。
// 用 (dist, hops, path) 全序标签的 Dijkstra 计算;扩展路径只会使标签变差
// (权重非负,零权时跳数严格增加),因此含零权环也必然终止。
// 不可达通过 RouteResult 的 { reachable: false } 单独表达,不使用任意大数冒充无穷。

export interface Version {
  epoch: number;
  seq: number;
}

/** 按 (epoch, seq) 字典序比较:返回负数/零/正数。 */
export function compareVersion(a: Version, b: Version): number {
  if (a.epoch !== b.epoch) return a.epoch < b.epoch ? -1 : 1;
  if (a.seq !== b.seq) return a.seq < b.seq ? -1 : 1;
  return 0;
}

export interface EdgeUpdate {
  from: string;
  to: string;
  weight: number;
  version: Version;
  /** true 表示删除该边(在 LSDB 中留下墓碑)。 */
  deleted?: boolean;
}

export interface EdgeRecord {
  from: string;
  to: string;
  weight: number;
  deleted: boolean;
  version: Version;
}

/** 同版本但内容冲突(权重/删除标志不同)时抛出。 */
export class VersionConflictError extends Error {
  readonly from: string;
  readonly to: string;
  constructor(from: string, to: string, version: Version) {
    super(
      `conflicting content for edge ${from} -> ${to} at version (${version.epoch}, ${version.seq})`,
    );
    this.name = "VersionConflictError";
    this.from = from;
    this.to = to;
  }
}

export class InvalidUpdateError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InvalidUpdateError";
  }
}

export interface BatchSummary {
  received: number;
  applied: number;
  duplicates: number;
  stale: number;
}

export interface Route {
  destination: string;
  distance: number;
  hops: number;
  /** 源节点自身为 null。 */
  nextHop: string | null;
  /** 完整节点 ID 序列(含源与目的)。 */
  path: string[];
  /** 路径最后一条边(进入目的节点的那条)的版本;源节点自身为 null。 */
  version: Version | null;
}

/** 不可达单独表达,不用任意大数冒充无穷。 */
export type RouteResult = { reachable: true; route: Route } | { reachable: false };

const SEP = String.fromCharCode(0);
const edgeKey = (from: string, to: string): string => from + SEP + to;

function validateUpdate(u: EdgeUpdate): void {
  for (const id of [u.from, u.to]) {
    if (typeof id !== "string" || id.length === 0 || id.includes(SEP)) {
      throw new InvalidUpdateError(`invalid node id: ${String(id)}`);
    }
  }
  if (!Number.isInteger(u.weight) || u.weight < 0) {
    throw new InvalidUpdateError(`weight must be a non-negative integer, got ${String(u.weight)}`);
  }
  const v = u.version;
  if (!v || !Number.isInteger(v.epoch) || v.epoch < 0 || !Number.isInteger(v.seq) || v.seq < 0) {
    throw new InvalidUpdateError(`invalid version: ${JSON.stringify(v)}`);
  }
}

interface Label {
  dist: number;
  hops: number;
  path: string[];
  version: Version | null;
}

/** 路由标签全序:距离 → 跳数 → 节点 ID 序列字典序。 */
function compareLabels(a: Label, b: Label): number {
  if (a.dist !== b.dist) return a.dist < b.dist ? -1 : 1;
  if (a.hops !== b.hops) return a.hops < b.hops ? -1 : 1;
  const n = Math.min(a.path.length, b.path.length);
  for (let i = 0; i < n; i++) {
    if (a.path[i] !== b.path[i]) return a.path[i] < b.path[i] ? -1 : 1;
  }
  return a.path.length - b.path.length;
}

interface CacheEntry {
  routes: Map<string, Route>;
  /** 该路由表实际用到的边(用于删除时的精确失效)。 */
  usedEdges: Set<string>;
}

export class LinkStateDatabase {
  private edges = new Map<string, EdgeRecord>();
  private cache = new Map<string, CacheEntry>();

  /**
   * 原子地应用一批更新:全部校验通过才提交;任何一条冲突都整批拒绝并抛错,
   * LSDB 保持批次应用前的状态。
   */
  applyBatch(updates: EdgeUpdate[]): BatchSummary {
    for (const u of updates) validateUpdate(u);

    // 影子副本:记录不可变(只整体替换),浅拷贝即安全。
    const shadow = new Map(this.edges);
    const summary: BatchSummary = { received: updates.length, applied: 0, duplicates: 0, stale: 0 };
    const changedKeys: string[] = [];

    for (const u of updates) {
      const key = edgeKey(u.from, u.to);
      const current = shadow.get(key);
      if (current) {
        const cmp = compareVersion(u.version, current.version);
        if (cmp < 0) {
          summary.stale++;
          continue;
        }
        if (cmp === 0) {
          if (current.weight === u.weight && current.deleted === !!u.deleted) {
            summary.duplicates++;
            continue;
          }
          throw new VersionConflictError(u.from, u.to, u.version);
        }
      }
      shadow.set(key, {
        from: u.from,
        to: u.to,
        weight: u.weight,
        deleted: !!u.deleted,
        version: { ...u.version },
      });
      summary.applied++;
      changedKeys.push(key);
    }

    this.edges = shadow;
    if (changedKeys.length > 0) this.invalidate(changedKeys);
    return summary;
  }

  /** 读取单条边(含墓碑)。 */
  getEdge(from: string, to: string): EdgeRecord | undefined {
    const rec = this.edges.get(edgeKey(from, to));
    return rec ? { ...rec, version: { ...rec.version } } : undefined;
  }

  /** 全部边记录(含墓碑),按 (from, to) 排序的拷贝。 */
  snapshotEdges(): EdgeRecord[] {
    return [...this.edges.values()]
      .map((r) => ({ ...r, version: { ...r.version } }))
      .sort((a, b) =>
        a.from === b.from ? (a.to < b.to ? -1 : 1) : a.from < b.from ? -1 : 1,
      );
  }

  /** 当前存活(未删除)边涉及的所有节点。 */
  nodes(): string[] {
    const set = new Set<string>();
    for (const rec of this.edges.values()) {
      if (rec.deleted) continue;
      set.add(rec.from);
      set.add(rec.to);
    }
    return [...set].sort();
  }

  /**
   * 查询 source 到所有可达节点的路由表(带缓存)。
   * 返回新 Map,键为目的节点 ID;不可达节点不出现。
   */
  routesFrom(source: string): Map<string, Route> {
    let entry = this.cache.get(source);
    if (!entry) {
      entry = this.computeRoutes(source);
      this.cache.set(source, entry);
    }
    return new Map([...entry.routes].map(([k, r]) => [k, cloneRoute(r)]));
  }

  /** 查询单个目的节点;不可达返回 { reachable: false }。 */
  routeTo(source: string, destination: string): RouteResult {
    const route = this.routesFrom(source).get(destination);
    return route ? { reachable: true, route } : { reachable: false };
  }

  /** 当前缓存的源节点数量(测试/观测用)。 */
  get cachedSourceCount(): number {
    return this.cache.size;
  }

  clearCache(): void {
    this.cache.clear();
  }

  private adjacency(): Map<string, EdgeRecord[]> {
    const adj = new Map<string, EdgeRecord[]>();
    for (const rec of this.edges.values()) {
      if (rec.deleted) continue;
      let list = adj.get(rec.from);
      if (!list) adj.set(rec.from, (list = []));
      list.push(rec);
    }
    return adj;
  }

  /**
   * 以 (dist, hops, path) 全序标签运行 Dijkstra。
   * 扩展路径不会改善标签(权重非负;零权时跳数严格增加),且该序满足保序性,
   * 因此节点出堆即最终,零权环下也必然终止。
   */
  private computeRoutes(source: string): CacheEntry {
    const adj = this.adjacency();
    const best = new Map<string, Label>();
    best.set(source, { dist: 0, hops: 0, path: [source], version: null });
    const settled = new Set<string>();

    for (;;) {
      let pick: string | null = null;
      let pickLabel: Label | null = null;
      for (const [node, label] of best) {
        if (settled.has(node)) continue;
        if (pickLabel === null || compareLabels(label, pickLabel) < 0) {
          pick = node;
          pickLabel = label;
        }
      }
      if (pick === null || pickLabel === null) break;
      settled.add(pick);

      for (const e of adj.get(pick) ?? []) {
        if (settled.has(e.to)) continue;
        const cand: Label = {
          dist: pickLabel.dist + e.weight,
          hops: pickLabel.hops + 1,
          path: [...pickLabel.path, e.to],
          version: e.version,
        };
        const cur = best.get(e.to);
        if (!cur || compareLabels(cand, cur) < 0) best.set(e.to, cand);
      }
    }

    const routes = new Map<string, Route>();
    const usedEdges = new Set<string>();
    for (const [dest, label] of best) {
      for (let i = 0; i + 1 < label.path.length; i++) {
        usedEdges.add(edgeKey(label.path[i], label.path[i + 1]));
      }
      routes.set(dest, {
        destination: dest,
        distance: label.dist,
        hops: label.hops,
        nextHop: label.path.length > 1 ? label.path[1] : null,
        path: [...label.path],
        version: label.version ? { ...label.version } : null,
      });
    }
    return { routes, usedEdges };
  }

  /**
   * 缓存失效:失效范围必须包含受变更边影响的路由。
   * 若缓存源 s 的路由用到了被变更的边(覆盖删除:边已不在图中),
   * 或 s 在变更后的图中仍能到达被变更边的起点(覆盖新增/改权:可能产生更优路径),
   * 则 s 的缓存失效。
   */
  private invalidate(changedKeys: string[]): void {
    if (this.cache.size === 0) return;
    const changed = changedKeys.map((key) => ({ key, from: key.slice(0, key.indexOf(SEP)) }));
    for (const [source, entry] of [...this.cache]) {
      const hit =
        changed.some((c) => entry.usedEdges.has(c.key)) ||
        changed.some((c) => this.isReachable(source, c.from));
      if (hit) this.cache.delete(source);
    }
  }

  private isReachable(source: string, target: string): boolean {
    if (source === target) return true;
    const adj = this.adjacency();
    const seen = new Set<string>([source]);
    const queue: string[] = [source];
    while (queue.length > 0) {
      const u = queue.shift() as string;
      for (const e of adj.get(u) ?? []) {
        if (e.to === target) return true;
        if (!seen.has(e.to)) {
          seen.add(e.to);
          queue.push(e.to);
        }
      }
    }
    return false;
  }
}

function cloneRoute(r: Route): Route {
  return { ...r, path: [...r.path], version: r.version ? { ...r.version } : null };
}
