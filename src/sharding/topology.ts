import { EventEmitter } from 'node:events';

export interface NodeInfo {
  id: string;
  region: string;
  zone: string;
}

export interface TopologyChange {
  type: 'add' | 'remove';
  id: string;
}

/**
 * Hierarchical topology: region -> zone -> node.
 *
 * Link cost model (lower = closer):
 *   same node 0, same zone 1, same region 5, cross-region 10,
 *   unless an explicit region-to-region latency override is set.
 */
export class Topology extends EventEmitter {
  static readonly COST_SAME_NODE = 0;
  static readonly COST_SAME_ZONE = 1;
  static readonly COST_SAME_REGION = 5;
  static readonly COST_CROSS_REGION = 10;

  private readonly nodes = new Map<string, NodeInfo>();
  private readonly regionLatency = new Map<string, number>();
  private disposed = false;

  addNode(info: NodeInfo): void {
    this.assertLive();
    if (this.nodes.has(info.id)) throw new Error(`duplicate node: ${info.id}`);
    if (info.id === '') throw new Error('node id must not be empty');
    if (info.region === '' || info.zone === '') {
      throw new Error(`node ${info.id} must have non-empty region and zone`);
    }
    this.nodes.set(info.id, { ...info });
    this.emit('change', { type: 'add', id: info.id } satisfies TopologyChange);
  }

  removeNode(id: string): void {
    this.assertLive();
    if (!this.nodes.delete(id)) throw new Error(`unknown node: ${id}`);
    this.emit('change', { type: 'remove', id } satisfies TopologyChange);
  }

  has(id: string): boolean {
    return this.nodes.has(id);
  }

  get(id: string): NodeInfo | undefined {
    return this.nodes.get(id);
  }

  list(): NodeInfo[] {
    return [...this.nodes.values()];
  }

  get size(): number {
    return this.nodes.size;
  }

  /** Override the cross-region link cost between two regions (symmetric). */
  setRegionLatency(regionA: string, regionB: string, ms: number): void {
    this.assertLive();
    if (!Number.isFinite(ms) || ms < 0) throw new RangeError('latency must be a non-negative number');
    this.regionLatency.set(Topology.regionKey(regionA, regionB), ms);
  }

  regionLatencyBetween(regionA: string, regionB: string): number | undefined {
    return this.regionLatency.get(Topology.regionKey(regionA, regionB));
  }

  /** Hierarchical link cost between two nodes. */
  cost(from: NodeInfo, to: NodeInfo): number {
    if (from.id === to.id) return Topology.COST_SAME_NODE;
    if (from.region === to.region && from.zone === to.zone) return Topology.COST_SAME_ZONE;
    if (from.region === to.region) return Topology.COST_SAME_REGION;
    return this.regionLatencyBetween(from.region, to.region) ?? Topology.COST_CROSS_REGION;
  }

  private assertLive(): void {
    if (this.disposed) throw new Error('topology has been disposed');
  }

  private static regionKey(a: string, b: string): string {
    return a < b ? `${a}|${b}` : `${b}|${a}`;
  }

  dispose(): void {
    this.nodes.clear();
    this.regionLatency.clear();
    this.removeAllListeners();
    this.disposed = true;
  }
}
