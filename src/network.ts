import { Router } from './router.ts';
import { DEFAULT_DAMPENING, type DampeningConfig } from './dampening.ts';
import type { LSA, NodeId } from './types.ts';

interface PhysicalLink {
  a: NodeId;
  b: NodeId;
  cost: number;
  up: boolean;
}

interface FloodItem {
  lsa: LSA;
  from: NodeId;
  at: NodeId;
}

export interface ForwardingCheck {
  loops: string[];
  blackholes: string[];
}

/**
 * 离散 tick 网络仿真器。
 *
 * 每个 tick 依次执行：
 *   1. 震荡惩罚衰减（解除抑制的路由器重新发起 LSA）
 *   2. 状态变化的路由器发起新 LSA
 *   3. LSA 沿可用链路逐跳泛洪，直至静默（建模假设：泛洪远快于 tick 粒度）
 *   4. LSDB 发生变化的路由器运行（增量）SPF，更新转发表
 *
 * 由于泛洪在同一 tick 内完成，所有路由器基于同一份 LSDB 计算，
 * 转发表天然一致，从机制上避免瞬时黑洞与环路。
 */
export class Network {
  readonly routers = new Map<NodeId, Router>();
  readonly logs: string[] = [];
  tickCount = 0;

  private readonly links = new Map<string, PhysicalLink>();
  private readonly pendingOriginate = new Set<NodeId>();
  private readonly floodQueue: FloodItem[] = [];
  private readonly dampCfg: DampeningConfig;

  constructor(dampCfg: DampeningConfig = DEFAULT_DAMPENING) {
    this.dampCfg = dampCfg;
  }

  addRouter(id: NodeId): Router {
    const router = new Router(id, () => this.tickCount, this.dampCfg);
    this.routers.set(id, router);
    return router;
  }

  addLink(a: NodeId, b: NodeId, cost: number): void {
    this.links.set(this.key(a, b), { a, b, cost, up: true });
    for (const [x, y] of [
      [a, b],
      [b, a],
    ] as const) {
      this.routers.get(x)!.interfaces.set(y, { cost, up: true });
      this.pendingOriginate.add(x);
    }
  }

  /** 断纤 */
  cutLink(a: NodeId, b: NodeId): void {
    this.setLinkUp(a, b, false);
  }

  /** 链路恢复 */
  restoreLink(a: NodeId, b: NodeId): void {
    this.setLinkUp(a, b, true);
  }

  /** 权值变更 */
  setCost(a: NodeId, b: NodeId, cost: number): void {
    const link = this.link(a, b);
    link.cost = cost;
    for (const [x, y] of [
      [a, b],
      [b, a],
    ] as const) {
      this.routers.get(x)!.onInterfaceEvent(y, link.up, cost);
      this.pendingOriginate.add(x);
    }
    this.logs.push(`[tick ${this.tickCount}] 网络: 链路 ${a}-${b} 权值变更为 ${cost}`);
  }

  private setLinkUp(a: NodeId, b: NodeId, up: boolean): void {
    const link = this.link(a, b);
    link.up = up;
    for (const [x, y] of [
      [a, b],
      [b, a],
    ] as const) {
      this.routers.get(x)!.onInterfaceEvent(y, up, link.cost);
      this.pendingOriginate.add(x);
    }
    this.logs.push(
      `[tick ${this.tickCount}] 网络: 链路 ${a}-${b} ${up ? '恢复' : '断开(断纤)'}`,
    );
  }

  private link(a: NodeId, b: NodeId): PhysicalLink {
    const link = this.links.get(this.key(a, b));
    if (!link) throw new Error(`链路不存在: ${a}-${b}`);
    return link;
  }

  private key(a: NodeId, b: NodeId): string {
    return a < b ? `${a}|${b}` : `${b}|${a}`;
  }

  /** 推进一个 tick */
  tick(): void {
    this.tickCount += 1;
    // 1. 惩罚衰减；解除抑制的路由器需要重新发起 LSA
    for (const router of this.routers.values()) {
      if (router.decayDampening()) this.pendingOriginate.add(router.id);
    }
    // 2. 发起 LSA
    for (const id of this.pendingOriginate) {
      const router = this.routers.get(id)!;
      const lsa = router.originate();
      const desc = lsa.links
        .map((l) => `${l.neighbor}${l.up ? '' : '(down)'}:${l.cost}`)
        .join(' ');
      this.logs.push(`[tick ${this.tickCount}] ${id}: 发起 LSA seq=${lsa.seq} [${desc}]`);
      for (const [nbr, iface] of router.interfaces) {
        if (iface.up) this.floodQueue.push({ lsa, from: id, at: nbr });
      }
    }
    this.pendingOriginate.clear();
    // 3. 泛洪直至静默
    while (this.floodQueue.length > 0) {
      const { lsa, from, at } = this.floodQueue.shift()!;
      const router = this.routers.get(at)!;
      if (!router.receive(lsa)) continue;
      this.logs.push(
        `[tick ${this.tickCount}] ${at}: 接收 ${lsa.origin} 的 LSA seq=${lsa.seq} (来自 ${from})`,
      );
      for (const [nbr, iface] of router.interfaces) {
        if (iface.up && nbr !== from) this.floodQueue.push({ lsa, from: at, at: nbr });
      }
    }
    // 4. SPF 重算
    for (const router of this.routers.values()) {
      router.runSpfIfNeeded();
    }
    this.drainRouterLogs();
  }

  private drainRouterLogs(): void {
    for (const router of this.routers.values()) {
      this.logs.push(...router.logs.splice(0));
    }
  }

  get converged(): boolean {
    return (
      this.pendingOriginate.size === 0 &&
      this.floodQueue.length === 0 &&
      [...this.routers.values()].every((r) => r.converged)
    );
  }

  /** 推进 tick 直至收敛，返回实际消耗的 tick 数 */
  runUntilConverged(maxTicks = 20): number {
    let ticks = 0;
    // 存在被抑制的链路时继续推进 tick，让惩罚衰减、解除抑制后重收敛
    while ((!this.converged || this.anySuppressed()) && ticks < maxTicks) {
      this.tick();
      ticks += 1;
    }
    this.drainRouterLogs();
    return ticks;
  }

  private anySuppressed(): boolean {
    for (const router of this.routers.values()) {
      for (const nbr of router.interfaces.keys()) {
        if (router.isSuppressed(nbr)) return true;
      }
    }
    return false;
  }

  /** 物理可达性（仅看 Up 链路），用于区分“合法不可达”与“黑洞” */
  private physicalReachability(): Map<NodeId, Set<NodeId>> {
    const result = new Map<NodeId, Set<NodeId>>();
    for (const id of this.routers.keys()) {
      const seen = new Set<NodeId>([id]);
      const queue = [id];
      while (queue.length > 0) {
        const cur = queue.shift()!;
        for (const link of this.links.values()) {
          if (!link.up) continue;
          let nbr: NodeId | null = null;
          if (link.a === cur) nbr = link.b;
          else if (link.b === cur) nbr = link.a;
          if (nbr !== null && !seen.has(nbr)) {
            seen.add(nbr);
            queue.push(nbr);
          }
        }
      }
      result.set(id, seen);
    }
    return result;
  }

  /**
   * 转发一致性校验：对每一对物理可达的 (源, 目的地)，
   * 沿各节点转发表逐跳模拟转发，检测路由环路和瞬时黑洞。
   */
  verifyForwarding(): ForwardingCheck {
    const reachable = this.physicalReachability();
    const loops: string[] = [];
    const blackholes: string[] = [];
    for (const src of this.routers.keys()) {
      for (const dest of this.routers.keys()) {
        if (src === dest || !reachable.get(src)!.has(dest)) continue;
        const visited = new Set<NodeId>([src]);
        let cur = src;
        for (let hops = 0; hops <= this.routers.size + 1; hops += 1) {
          if (cur === dest) break;
          const route = this.routers.get(cur)!.routes.get(dest);
          if (!route || route.nextHop === null) {
            blackholes.push(`${src}->${dest}: 黑洞@${cur}(无转发表项)`);
            break;
          }
          if (visited.has(route.nextHop)) {
            loops.push(`${src}->${dest}: 环路@${cur}->${route.nextHop}`);
            break;
          }
          visited.add(route.nextHop);
          cur = route.nextHop;
        }
      }
    }
    return { loops, blackholes };
  }
}
