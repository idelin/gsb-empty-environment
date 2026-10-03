import { LSDB, type ChangedEdge } from './lsdb.ts';
import { buildRoutes, fullSpf, incrementalSpf, type SpfResult } from './spf.ts';
import { DEFAULT_DAMPENING, LinkDampener, type DampeningConfig } from './dampening.ts';
import type { LSA, NodeId, Route } from './types.ts';

interface InterfaceState {
  cost: number;
  up: boolean;
}

/**
 * 路由器：维护本地接口状态、LSDB、转发表；
 * 负责发起/接收 LSA，并在 LSDB 变化时触发（增量）SPF 重算。
 */
export class Router {
  readonly id: NodeId;
  readonly lsdb = new LSDB();
  readonly routes = new Map<NodeId, Route>();
  readonly interfaces = new Map<NodeId, InterfaceState>();
  readonly logs: string[] = [];
  /** 两次 SPF 的最小间隔（tick），用于防震荡退避；0 表示立即计算 */
  spfMinIntervalTicks = 0;

  private seq = 0;
  private spfDirty = true;
  private lastSpfTick = -Infinity;
  private lastDist: Map<NodeId, number> | null = null;
  private lastPrev: Map<NodeId, NodeId | null> | null = null;
  private readonly dampener: LinkDampener;
  private readonly tickRef: () => number;

  constructor(id: NodeId, tickRef: () => number, dampCfg: DampeningConfig = DEFAULT_DAMPENING) {
    this.id = id;
    this.tickRef = tickRef;
    this.dampener = new LinkDampener(id, dampCfg, (m) => this.log(m));
  }

  private log(msg: string): void {
    this.logs.push(`[tick ${this.tickRef()}] ${this.id}: ${msg}`);
  }

  /** 接口事件（由网络层在断纤/恢复/改权时调用） */
  onInterfaceEvent(neighbor: NodeId, up: boolean, cost?: number): void {
    const iface = this.interfaces.get(neighbor);
    if (!iface) return;
    iface.up = up;
    if (cost !== undefined) iface.cost = cost;
    this.dampener.onFlap(neighbor, up);
    this.log(
      `接口事件: 链路 ${this.id}-${neighbor} ${up ? 'Up' : 'Down'}` +
        (cost !== undefined ? ` cost=${cost}` : ''),
    );
  }

  /** 发起新的 LSA（序列号自增），被抑制的链路按 Down 通告 */
  originate(): LSA {
    this.seq += 1;
    const links = [...this.interfaces.entries()].map(([neighbor, i]) => ({
      neighbor,
      cost: i.cost,
      up: i.up && this.dampener.isUsable(neighbor),
    }));
    const lsa: LSA = { origin: this.id, seq: this.seq, age: 0, links };
    this.lsdb.insert(lsa);
    this.spfDirty = true;
    return lsa;
  }

  /** 接收 LSA；序列号过期则拒绝。返回是否接受了更新 */
  receive(lsa: LSA): boolean {
    const accepted = this.lsdb.insert(lsa);
    if (accepted) this.spfDirty = true;
    return accepted;
  }

  /** 每个 tick 衰减震荡惩罚；有链路解除抑制时返回 true（需重新发起 LSA） */
  decayDampening(): boolean {
    return this.dampener.decay().length > 0;
  }

  isSuppressed(neighbor: NodeId): boolean {
    return this.dampener.isSuppressed(neighbor);
  }

  /** LSDB 有变化时运行 SPF（优先增量），返回计算结果 */
  runSpfIfNeeded(): SpfResult | null {
    if (!this.spfDirty) return null;
    const now = this.tickRef();
    if (now - this.lastSpfTick < this.spfMinIntervalTicks) {
      this.log(`SPF 退避: 距上次计算不足 ${this.spfMinIntervalTicks} tick, 延迟重算(防震荡)`);
      return null;
    }
    const changes: ChangedEdge[] = this.lsdb.diffSinceComputed();
    const topo = this.lsdb.toTopology();
    const isFirstCompute = this.lastDist === null || this.lastPrev === null;
    let result: SpfResult;
    if (isFirstCompute) {
      result = fullSpf(topo, this.id);
    } else {
      result = incrementalSpf(topo, this.id, this.lastDist, this.lastPrev, changes);
    }
    this.lastDist = result.dist;
    this.lastPrev = result.prev;
    this.lastSpfTick = now;
    this.routes.clear();
    for (const [dest, route] of buildRoutes(this.id, result.dist, result.prev)) {
      this.routes.set(dest, route);
    }
    const trigger =
      changes.map((c) => `${c.u}-${c.v}:${c.kind}`).join(' ') ||
      (isFirstCompute ? '首次计算' : 'LSA 刷新(拓扑无变化)');
    this.log(
      `SPF 重算(mode=${result.mode}, 重算目的地 ${result.recomputed}/${topo.size}) 触发: ${trigger}`,
    );
    this.spfDirty = false;
    return result;
  }

  get converged(): boolean {
    return !this.spfDirty;
  }
}
