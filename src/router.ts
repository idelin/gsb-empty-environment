import { LinkStateDatabase, type BatchResult, type EdgeUpdate } from "./lsdb.ts";
import { computeRoutes, type Route } from "./spf.ts";

const edgeKey = (src: string, dst: string): string => JSON.stringify([src, dst]);

/**
 * 路由器：LSDB + 最短路查询缓存。
 *
 * 缓存失效规则（按变更类型分级）：
 * - 删除边 / 权重增大：只会令经过该边的路径变差或消失，
 *   精确失效所有缓存路径经过该 (src,dst) 的源——被删边影响的路径
 *   必然包含在内。
 * - 新增边 / 权重减小：可能让任意源出现更优路径，无法局部界定，
 *   保守地清空全部缓存。
 */
export class Router {
  readonly lsdb = new LinkStateDatabase();
  private cache = new Map<string, Map<string, Route>>();

  /** 应用批次；冲突时 LSDB 整体回滚，缓存保持不动。 */
  applyBatch(updates: EdgeUpdate[]): BatchResult {
    const result = this.lsdb.applyBatch(updates);
    if (result.changes.length > 0) this.invalidate(result.changes);
    return result;
  }

  private invalidate(changes: BatchResult["changes"]): void {
    let widenAll = false;
    const narrowed: string[] = [];
    for (const c of changes) {
      const worsening =
        c.after.deleted ||
        (c.before !== null && !c.before.deleted && c.after.weight > c.before.weight);
      if (worsening) narrowed.push(edgeKey(c.src, c.dst));
      else widenAll = true; // 新增、墓碑复活、权重减小
    }
    if (widenAll) {
      this.cache.clear();
      return;
    }
    const badEdges = new Set(narrowed);
    for (const [src, routes] of this.cache) {
      for (const route of routes.values()) {
        if (!route.reachable) continue;
        const p = route.path;
        let hit = false;
        for (let i = 0; i + 1 < p.length; i++) {
          if (badEdges.has(edgeKey(p[i], p[i + 1]))) {
            hit = true;
            break;
          }
        }
        if (hit) {
          this.cache.delete(src);
          break;
        }
      }
    }
  }

  /** 查询指定源到所有已知节点的路由（带缓存）。 */
  routesFrom(source: string): Map<string, Route> {
    let routes = this.cache.get(source);
    if (!routes) {
      routes = computeRoutes(this.lsdb.adjacency(), this.lsdb.nodes(), source);
      this.cache.set(source, routes);
    }
    return routes;
  }

  /** 查询指定源到单个目的地的路由；不可达时返回 { reachable: false }。 */
  routeTo(source: string, dst: string): Route {
    return this.routesFrom(source).get(dst) ?? { reachable: false };
  }

  /** 测试/调试用：当前缓存的源数量。 */
  get cachedSourceCount(): number {
    return this.cache.size;
  }
}
