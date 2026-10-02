/**
 * Hierarchical Token Bucket (HTB) tree bandwidth allocator.
 *
 * Model (discrete ticks, units are MB/s):
 * - Each node has a guaranteed `rate`, a hard `ceil`, and a `weight` used
 *   when contending for spare (borrowed) bandwidth.
 * - Leaves represent business lines; internal nodes represent departments.
 * - Each tick, leaves publish `demand`. The tree computes a *target*
 *   allocation via hierarchical water-filling: guaranteed rates first, then
 *   spare capacity is lent to siblings proportional to weight, capped by ceil.
 * - Applied (`current`) allocations move toward the target smoothly:
 *   decreases are limited by `reclaimAlpha` per tick, and increases may only
 *   consume capacity that has actually been freed, so the root budget is
 *   never exceeded on any tick.
 */

export interface NodeOptions {
  name: string;
  /** Guaranteed bandwidth (MB/s). */
  rate: number;
  /** Maximum bandwidth (MB/s), including borrowed. Defaults to parent ceil. */
  ceil?: number;
  /** Weight for sharing spare bandwidth with siblings. Default 1. */
  weight?: number;
  children?: NodeOptions[];
}

export interface SmoothingOptions {
  /** Fraction of the remaining gap reclaimed per tick when cutting back (0,1]. */
  reclaimAlpha?: number;
  /** Fraction of the remaining gap granted per tick when growing (0,1]. */
  rampAlpha?: number;
  /** Snap to target when the remaining gap is within this epsilon (MB/s). */
  epsilon?: number;
}

interface ResolvedSmoothing {
  reclaimAlpha: number;
  rampAlpha: number;
  epsilon: number;
}

export class HtbNode {
  readonly name: string;
  readonly rate: number;
  readonly ceil: number;
  readonly weight: number;
  readonly parent: HtbNode | null;
  readonly children: HtbNode[] = [];

  /** Demand for the current tick (leaves: set via setDemand; internal: aggregated). */
  demand = 0;
  /** Target allocation computed for this tick. */
  target = 0;
  /** Actually applied (smoothed) allocation. */
  current = 0;

  constructor(options: NodeOptions, parent: HtbNode | null = null) {
    this.name = options.name;
    this.rate = options.rate;
    this.weight = options.weight ?? 1;
    this.parent = parent;
    const parentCeil = parent ? parent.ceil : Infinity;
    this.ceil = Math.min(options.ceil ?? parentCeil, parentCeil);
    for (const child of options.children ?? []) {
      this.children.push(new HtbNode(child, this));
    }
  }

  get isLeaf(): boolean {
    return this.children.length === 0;
  }

  find(path: string[]): HtbNode {
    if (path.length === 0) return this;
    const [head, ...rest] = path;
    const child = this.children.find((c) => c.name === head);
    if (!child) throw new Error(`unknown node: ${path.join("/")}`);
    return child.find(rest);
  }

  leaves(): HtbNode[] {
    if (this.isLeaf) return [this];
    return this.children.flatMap((c) => c.leaves());
  }
}

export class HtbAllocator {
  readonly root: HtbNode;
  private readonly smoothing: ResolvedSmoothing;

  constructor(root: NodeOptions, smoothing: SmoothingOptions = {}) {
    this.root = new HtbNode(root);
    this.smoothing = {
      reclaimAlpha: smoothing.reclaimAlpha ?? 0.5,
      rampAlpha: smoothing.rampAlpha ?? 1,
      epsilon: smoothing.epsilon ?? 0.5,
    };
  }

  /** Set the demand (MB/s) of a leaf for the next tick. */
  setDemand(path: string[], demand: number): void {
    if (demand < 0) throw new Error("demand must be >= 0");
    const node = this.root.find(path);
    if (!node.isLeaf) throw new Error(`${path.join("/")} is not a leaf`);
    node.demand = demand;
  }

  /**
   * Advance one tick: recompute targets, then move applied allocations
   * smoothly toward them. Returns a snapshot of leaf allocations.
   */
  tick(): Map<string, number> {
    this.aggregateDemand(this.root);
    this.root.target = Math.min(this.root.demand, this.root.rate);
    this.distribute(this.root, this.root.target);
    this.applySmoothing();
    const snapshot = new Map<string, number>();
    for (const leaf of this.root.leaves()) {
      snapshot.set(this.pathOf(leaf), leaf.current);
    }
    return snapshot;
  }

  private pathOf(node: HtbNode): string {
    const parts: string[] = [];
    let cursor: HtbNode | null = node;
    while (cursor && cursor.parent) {
      parts.unshift(cursor.name);
      cursor = cursor.parent;
    }
    return parts.join("/");
  }

  private aggregateDemand(node: HtbNode): number {
    if (node.isLeaf) {
      node.demand = Math.min(node.demand, node.ceil);
      return node.demand;
    }
    const sum = node.children.reduce((acc, c) => acc + this.aggregateDemand(c), 0);
    node.demand = Math.min(sum, node.ceil);
    return node.demand;
  }

  /**
   * Hierarchical water-filling: split `capacity` among the children of `node`.
   * Guaranteed rates are served first; spare capacity is lent to siblings
   * proportional to weight, capped by each child ceil and demand.
   */
  private distribute(node: HtbNode, capacity: number): void {
    if (node.isLeaf) return;
    const shares = this.share(node.children, capacity);
    for (let i = 0; i < node.children.length; i++) {
      const child = node.children[i];
      child.target = shares[i];
      this.distribute(child, shares[i]);
    }
  }

  /** Split `capacity` among sibling nodes (guaranteed first, then weighted spare). */
  private share(nodes: HtbNode[], capacity: number): number[] {
    const alloc = nodes.map((n) => Math.min(n.demand, n.rate));
    let remaining = capacity - alloc.reduce((a, b) => a + b, 0);

    if (remaining < 0) {
      // Guaranteed rates oversubscribe capacity: reclaim proportionally to
      // weight, never below zero.
      let deficit = -remaining;
      let contenders = nodes.filter((_, i) => alloc[i] > 0);
      while (deficit > 1e-9 && contenders.length > 0) {
        const roundDeficit = deficit;
        const totalWeight = contenders.reduce((a, n) => a + n.weight, 0);
        const next: HtbNode[] = [];
        for (const n of contenders) {
          const i = nodes.indexOf(n);
          const cut = Math.min(alloc[i], (roundDeficit * n.weight) / totalWeight);
          alloc[i] -= cut;
          deficit -= cut;
          if (alloc[i] > 1e-9) next.push(n);
        }
        contenders = next;
      }
      return alloc;
    }

    // Lend spare capacity to siblings with unmet demand, weighted, ceil-capped.
    let contenders = nodes.filter(
      (n, i) => alloc[i] < Math.min(n.demand, n.ceil) - 1e-9,
    );
    while (remaining > 1e-9 && contenders.length > 0) {
      const roundRemaining = remaining;
      const totalWeight = contenders.reduce((a, n) => a + n.weight, 0);
      const next: HtbNode[] = [];
      for (const n of contenders) {
        const i = nodes.indexOf(n);
        const cap = Math.min(n.demand, n.ceil) - alloc[i];
        const grant = Math.min(cap, (roundRemaining * n.weight) / totalWeight);
        alloc[i] += grant;
        remaining -= grant;
        if (cap - grant > 1e-9) next.push(n);
      }
      contenders = next;
    }
    return alloc;
  }

  /**
   * Move applied allocations toward targets without ever exceeding the root
   * budget: decreases are gradual (reclaimAlpha), and increases may only use
   * capacity freed so far plus current slack.
   */
  private applySmoothing(): void {
    const { reclaimAlpha, rampAlpha, epsilon } = this.smoothing;
    const leaves = this.root.leaves();
    const budget = this.root.rate;

    const move = (node: HtbNode, alpha: number): number => {
      const gap = node.target - node.current;
      if (Math.abs(gap) <= epsilon) return node.target;
      return node.current + gap * alpha;
    };

    // Phase 1: shrink over-allocated leaves gradually.
    for (const leaf of leaves) {
      if (leaf.target < leaf.current) {
        leaf.current = move(leaf, reclaimAlpha);
      }
    }

    // Phase 2: grow under-allocated leaves, bounded by real free capacity.
    const growing = leaves.filter((l) => l.target > l.current);
    let free = budget - leaves.reduce((a, l) => a + l.current, 0);
    for (const leaf of growing) {
      if (free <= 1e-9) break;
      const wanted = move(leaf, rampAlpha) - leaf.current;
      const grant = Math.min(wanted, Math.max(0, free));
      leaf.current += grant;
      free -= grant;
    }
  }
}
