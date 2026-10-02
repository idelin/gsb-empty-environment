/**
 * Hierarchical Token Bucket (HTB) tree bandwidth allocator.
 *
 * Model:
 * - Inner nodes represent departments, leaves represent business lines.
 * - Each node has a guaranteed `rate` and a max `ceil` (MB/s).
 * - Idle guaranteed bandwidth of a sibling can be borrowed by siblings
 *   (recursively up the tree), capped by `ceil`.
 * - When total demand exceeds capacity, bandwidth is reclaimed
 *   proportionally to each child's weight (its guaranteed `rate`).
 * - Reclaims are smoothed over time (EWMA) so bursting borrowers are
 *   suppressed gradually instead of being cut off instantly.
 * - Each leaf enforces its current allocation with a token bucket.
 */

export interface HtbNodeConfig {
  name: string;
  /** Guaranteed bandwidth in MB/s. Also acts as the reclaim weight. */
  rate: number;
  /** Maximum bandwidth in MB/s. Defaults to the parent's ceil. */
  ceil?: number;
  children?: HtbNodeConfig[];
}

export interface HtbAllocatorOptions {
  /** EWMA time constant (s) for allocation increases. Default 0.2. */
  attackTau?: number;
  /** EWMA time constant (s) for allocation decreases (reclaim). Default 1.0. */
  releaseTau?: number;
  /** Token bucket depth in seconds of `ceil`. Default 1. */
  burstSeconds?: number;
}

export interface NodeSnapshot {
  name: string;
  rate: number;
  ceil: number;
  demand: number;
  /** Instantaneous fair-share target from the last tick (MB/s). */
  target: number;
  /** Smoothed allocation actually enforced (MB/s). */
  alloc: number;
  /** Leaf token bucket level in MB (undefined for inner nodes). */
  tokensMb?: number;
}

export class TokenBucket {
  readonly capacityMb: number;
  tokensMb: number;

  constructor(capacityMb: number) {
    this.capacityMb = capacityMb;
    this.tokensMb = capacityMb;
  }

  refill(amountMb: number): void {
    this.tokensMb = Math.min(this.capacityMb, this.tokensMb + amountMb);
  }

  /** Returns how many MB of the request are allowed. */
  consume(requestMb: number): number {
    const allowed = Math.min(Math.max(0, requestMb), this.tokensMb);
    this.tokensMb -= allowed;
    return allowed;
  }
}

class HtbNode {
  readonly name: string;
  readonly rate: number;
  readonly ceil: number;
  readonly parent: HtbNode | null;
  readonly children: HtbNode[] = [];

  /** Requested bandwidth in MB/s (leaves only). */
  demand = 0;
  /** Fair-share target computed by the last tick (MB/s). */
  target = 0;
  /** Smoothed, actually enforced allocation (MB/s). */
  alloc = 0;
  /** Token bucket, present on leaves only. */
  bucket: TokenBucket | null = null;

  constructor(config: HtbNodeConfig, parent: HtbNode | null, burstSeconds: number) {
    this.name = config.name;
    this.rate = config.rate;
    this.parent = parent;
    this.ceil = config.ceil ?? parent?.ceil ?? config.rate;
    for (const child of config.children ?? []) {
      this.children.push(new HtbNode(child, this, burstSeconds));
    }
    if (this.isLeaf) {
      this.bucket = new TokenBucket(this.ceil * burstSeconds);
    }
  }

  get isLeaf(): boolean {
    return this.children.length === 0;
  }

  /** Total demand of the subtree in MB/s. */
  subtreeDemand(): number {
    if (this.isLeaf) return this.demand;
    return this.children.reduce((sum, child) => sum + child.subtreeDemand(), 0);
  }
}

const EPS = 1e-9;

/**
 * Weighted water-filling: distribute `capacity` among items with the given
 * weights and per-item caps. Items receive shares proportional to their
 * weight until their cap binds; freed capacity is redistributed to the rest.
 */
export function distribute(weights: number[], caps: number[], capacity: number): number[] {
  const alloc = weights.map(() => 0);
  let remaining = Math.max(0, capacity);
  const active = new Set<number>();
  for (let i = 0; i < weights.length; i++) {
    if (weights[i] > 0 && caps[i] > EPS) active.add(i);
  }
  while (remaining > EPS && active.size > 0) {
    let weightSum = 0;
    for (const i of active) weightSum += weights[i];
    let cappedAny = false;
    for (const i of [...active]) {
      const share = (remaining * weights[i]) / weightSum;
      const room = caps[i] - alloc[i];
      if (share >= room - EPS) {
        alloc[i] = caps[i];
        remaining -= room;
        active.delete(i);
        cappedAny = true;
      }
    }
    if (!cappedAny) {
      for (const i of active) {
        alloc[i] += (remaining * weights[i]) / weightSum;
      }
      remaining = 0;
    }
  }
  return alloc;
}

export class HtbAllocator {
  private readonly root: HtbNode;
  private readonly byName = new Map<string, HtbNode>();
  private readonly attackTau: number;
  private readonly releaseTau: number;

  constructor(config: HtbNodeConfig, options: HtbAllocatorOptions = {}) {
    this.attackTau = options.attackTau ?? 0.2;
    this.releaseTau = options.releaseTau ?? 1.0;
    const burstSeconds = options.burstSeconds ?? 1;
    this.root = new HtbNode({ ceil: config.rate, ...config }, null, burstSeconds);
    const walk = (node: HtbNode): void => {
      if (this.byName.has(node.name)) {
        throw new Error(`duplicate node name: ${node.name}`);
      }
      this.byName.set(node.name, node);
      node.children.forEach(walk);
    };
    walk(this.root);
  }

  /** Set the requested bandwidth (MB/s) of a leaf business line. */
  setDemand(leafName: string, mbps: number): void {
    const node = this.getNode(leafName);
    if (!node.isLeaf) {
      throw new Error(`demand can only be set on a leaf, got inner node: ${leafName}`);
    }
    if (!(mbps >= 0)) throw new Error(`demand must be >= 0, got ${mbps}`);
    node.demand = mbps;
  }

  /** Demand of a leaf, or aggregated demand of a subtree (MB/s). */
  demandOf(name: string): number {
    return this.getNode(name).subtreeDemand();
  }

  /**
   * Advance the simulation by `dt` seconds: recompute fair-share targets,
   * smooth enforced allocations toward them, and refill leaf token buckets.
   */
  tick(dt: number): void {
    if (!(dt > 0)) throw new Error(`dt must be > 0, got ${dt}`);
    this.computeTargets(this.root, this.root.rate);
    const smooth = (node: HtbNode): void => {
      if (node.isLeaf) {
        const tau = node.target > node.alloc ? this.attackTau : this.releaseTau;
        const alpha = 1 - Math.exp(-dt / tau);
        node.alloc += (node.target - node.alloc) * alpha;
        node.bucket!.refill(node.alloc * dt);
      } else {
        node.children.forEach(smooth);
        node.alloc = node.children.reduce((sum, child) => sum + child.alloc, 0);
      }
    };
    smooth(this.root);
  }

  /** Consume `mb` MB from a leaf's token bucket; returns the allowed amount. */
  consume(leafName: string, mb: number): number {
    const node = this.getNode(leafName);
    if (!node.bucket) {
      throw new Error(`consume is only valid on a leaf, got inner node: ${leafName}`);
    }
    return node.bucket.consume(mb);
  }

  /** Snapshot of one node (or the whole tree, bottom-up order). */
  snapshot(name?: string): NodeSnapshot[] {
    const out: NodeSnapshot[] = [];
    const emit = (node: HtbNode): void => {
      node.children.forEach(emit);
      out.push({
        name: node.name,
        rate: node.rate,
        ceil: node.ceil,
        demand: node.subtreeDemand(),
        target: node.target,
        alloc: node.alloc,
        tokensMb: node.bucket?.tokensMb,
      });
    };
    emit(name === undefined ? this.root : this.getNode(name));
    return out;
  }

  allocOf(name: string): number {
    return this.getNode(name).alloc;
  }

  targetOf(name: string): number {
    return this.getNode(name).target;
  }

  private getNode(name: string): HtbNode {
    const node = this.byName.get(name);
    if (!node) throw new Error(`unknown node: ${name}`);
    return node;
  }

  /**
   * Recursive fair-share: a node distributes its capacity among children
   * proportional to their guaranteed rates, capped by ceil and demand.
   * Unused child capacity is inherited by siblings (borrowing); when the
   * parent's capacity is insufficient, shares shrink proportionally
   * (weighted reclaim).
   */
  private computeTargets(node: HtbNode, capacity: number): void {
    const cap = Math.min(node.ceil, capacity);
    if (node.isLeaf) {
      node.target = Math.min(node.demand, cap);
      return;
    }
    const weights = node.children.map((child) => child.rate);
    const caps = node.children.map((child) =>
      Math.min(child.ceil, child.subtreeDemand()),
    );
    const shares = distribute(weights, caps, cap);
    node.children.forEach((child, i) => this.computeTargets(child, shares[i]));
    node.target = node.children.reduce((sum, child) => sum + child.target, 0);
  }
}
