/**
 * Broadphase：物理引擎粗测阶段碰撞对筛选器。
 *
 * 内部以增量动态 AABB 树维护所有实体的"膨胀包围盒"（fat AABB）：
 * - 实体小幅移动时被 fat AABB 余量吸收，不触发树结构变更；
 * - 移动越界时增量重插对应叶节点（O(log N)），并记入 moved 缓冲；
 * - collectPairs() 仅对 moved 代理做树查询并去重，产出本帧潜在碰撞对；
 * - computeAllPairs() 通过树内自相交遍历全量枚举重叠对。
 *
 * 产出的候选对基于实体精确包围盒（tight AABB）过滤，可直接移交窄测阶段。
 */
import { cloneAabb, contains, expanded, overlaps } from "./aabb.ts";
import type { AABB } from "./aabb.ts";
import { DynamicAabbTree } from "./dynamicAabbTree.ts";

export type CollisionPair<T> = [T, T];

function copyAabb(target: AABB, source: AABB): void {
  target.minX = source.minX;
  target.minY = source.minY;
  target.minZ = source.minZ;
  target.maxX = source.maxX;
  target.maxY = source.maxY;
  target.maxZ = source.maxZ;
}

export class Broadphase<T = number> {
  private readonly margin: number;
  private readonly tree = new DynamicAabbTree<T>();
  private readonly entityToProxy = new Map<T, number>();
  private readonly proxyToEntity = new Map<number, T>();
  private readonly fatAabbs = new Map<number, AABB>();
  private readonly tightAabbs = new Map<number, AABB>();
  private readonly movedProxies = new Set<number>();
  /** collectPairs 复用的去重集合，避免每帧分配。 */
  private readonly seenPairs = new Set<number>();

  constructor(margin = 0.1) {
    this.margin = margin;
  }

  get size(): number {
    return this.entityToProxy.size;
  }

  get treeHeight(): number {
    return this.tree.getHeight();
  }

  has(entity: T): boolean {
    return this.entityToProxy.has(entity);
  }

  /** 注册实体及其初始包围盒。 */
  add(entity: T, aabb: AABB): void {
    if (this.entityToProxy.has(entity)) {
      throw new Error(`entity already exists: ${String(entity)}`);
    }
    const fat = expanded(aabb, this.margin);
    const proxy = this.tree.createProxy(fat, entity);
    this.entityToProxy.set(entity, proxy);
    this.proxyToEntity.set(proxy, entity);
    this.fatAabbs.set(proxy, fat);
    this.tightAabbs.set(proxy, cloneAabb(aabb));
    this.movedProxies.add(proxy);
  }

  /** 注销实体。 */
  remove(entity: T): void {
    const proxy = this.entityToProxy.get(entity);
    if (proxy === undefined) {
      throw new Error(`unknown entity: ${String(entity)}`);
    }
    this.tree.destroyProxy(proxy);
    this.entityToProxy.delete(entity);
    this.proxyToEntity.delete(proxy);
    this.fatAabbs.delete(proxy);
    this.tightAabbs.delete(proxy);
    this.movedProxies.delete(proxy);
  }

  /**
   * 实体移动：更新其精确包围盒。
   * 若新包围盒仍被 fat AABB 包含则仅记录移动；否则增量重插树节点。
   */
  move(entity: T, aabb: AABB): void {
    const proxy = this.entityToProxy.get(entity);
    if (proxy === undefined) {
      throw new Error(`unknown entity: ${String(entity)}`);
    }
    copyAabb(this.tightAabbs.get(proxy) as AABB, aabb);
    const fat = this.fatAabbs.get(proxy) as AABB;
    if (!contains(fat, aabb)) {
      const newFat = expanded(aabb, this.margin);
      this.fatAabbs.set(proxy, newFat);
      this.tree.moveProxy(proxy, newFat);
    }
    this.movedProxies.add(proxy);
  }

  /**
   * 增量收集潜在碰撞对：仅查询自上次调用以来移动过的代理。
   * 返回去重后的实体对（基于精确包围盒相交），并清空 moved 缓冲。
   * 当移动代理占比过半时，自动切换为树内自相交遍历（常数开销更低）。
   */
  collectPairs(): Array<CollisionPair<T>> {
    if (this.movedProxies.size === 0) return [];
    if (this.movedProxies.size * 2 >= this.entityToProxy.size) {
      this.movedProxies.clear();
      return this.computeAllPairs();
    }
    const pairs: Array<CollisionPair<T>> = [];
    const seen = this.seenPairs;
    seen.clear();
    let currentProxy = -1;
    let currentTight: AABB | null = null;
    const onLeaf = (_otherEntity: T, otherProxy: number): void => {
      if (otherProxy === currentProxy) return;
      const lo = Math.min(currentProxy, otherProxy);
      const hi = Math.max(currentProxy, otherProxy);
      const key = lo * 1048576 + hi;
      if (seen.has(key)) return;
      seen.add(key);
      const otherTight = this.tightAabbs.get(otherProxy) as AABB;
      if (!overlaps(currentTight as AABB, otherTight)) return;
      pairs.push([
        this.proxyToEntity.get(currentProxy) as T,
        this.proxyToEntity.get(otherProxy) as T,
      ]);
    };
    for (const proxy of this.movedProxies) {
      currentProxy = proxy;
      currentTight = this.tightAabbs.get(proxy) as AABB;
      this.tree.query(this.fatAabbs.get(proxy) as AABB, onLeaf);
    }
    this.movedProxies.clear();
    return pairs;
  }

  /**
   * 全量收集潜在碰撞对：树内自相交遍历，O(N log N + K)。
   * 不依赖 moved 缓冲，可用于校验或全量重建。
   */
  computeAllPairs(): Array<CollisionPair<T>> {
    const pairs: Array<CollisionPair<T>> = [];
    this.tree.forEachOverlappingPair((a, b) => {
      const proxyA = this.entityToProxy.get(a) as number;
      const proxyB = this.entityToProxy.get(b) as number;
      const tightA = this.tightAabbs.get(proxyA) as AABB;
      const tightB = this.tightAabbs.get(proxyB) as AABB;
      if (overlaps(tightA, tightB)) {
        pairs.push([a, b]);
      }
    });
    return pairs;
  }
}
