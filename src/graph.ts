import type { FieldState } from "./fields.ts";

export class GraphError extends Error {
  code: string;
  /** JSON Pointer locating the offending input. */
  path: string;
  source?: string;
  eventId?: string;
  batch?: string;
  constructor(code: string, message: string, path: string) {
    super(message);
    this.name = "GraphError";
    this.code = code;
    this.path = path;
  }
}

export interface SubtreeStats {
  /** Number of nodes in the subtree, including this node. */
  nodeCount: number;
  /** Sum of the numeric `weight` field over the subtree. */
  weightSum: number;
}

export interface EntityNode {
  id: string;
  /** Mounted parent (effective parent that exists), null for roots/orphans. */
  parentId: string | null;
  /** Effective parent that does not exist yet; node sits in the orphan queue. */
  pendingParentId: string | null;
  children: Set<string>;
  /** Field name -> lineage state. `@parent` is the reserved parent-link field. */
  fields: Map<string, FieldState>;
  /** source id -> latest applied version from that source. */
  sources: Map<string, number>;
  /** Own numeric weight (effective value of the `weight` field, or 0). */
  selfWeight: number;
  /** Aggregate over the subtree rooted here (self included). */
  stats: SubtreeStats;
}

export const PARENT_FIELD = "@parent";
export const WEIGHT_FIELD = "weight";

function neg(s: SubtreeStats): SubtreeStats {
  return { nodeCount: -s.nodeCount, weightSum: -s.weightSum };
}

/**
 * Hierarchical graph with stable entity ids.
 * Children may exist before parents (orphan queue); subtree stats are
 * maintained incrementally and only propagate along affected ancestor chains.
 */
export class Graph {
  nodes = new Map<string, EntityNode>();
  /** Orphan queue: orphan id -> pending parent id. */
  orphans = new Map<string, string>();
  /** Ids whose stats were touched since last `drainTouched` (test/audit aid). */
  private touched = new Set<string>();

  has(id: string): boolean {
    return this.nodes.has(id);
  }

  get(id: string): EntityNode {
    const n = this.nodes.get(id);
    if (!n) throw new GraphError("NO_ENTITY", `unknown entity ${id}`, "");
    return n;
  }

  addEntity(id: string): EntityNode {
    const node: EntityNode = {
      id,
      parentId: null,
      pendingParentId: null,
      children: new Set(),
      fields: new Map(),
      sources: new Map(),
      selfWeight: 0,
      stats: { nodeCount: 1, weightSum: 0 },
    };
    this.nodes.set(id, node);
    return node;
  }

  /** True if mounting `childId` under `newParentId` would create a cycle. */
  wouldCycle(childId: string, newParentId: string): boolean {
    if (childId === newParentId) return true;
    let cur: string | null = newParentId;
    while (cur !== null) {
      if (cur === childId) return true;
      cur = this.nodes.get(cur)?.parentId ?? null;
    }
    return false;
  }

  /** Add delta to `startId` and every ancestor — only the affected chain. */
  private propagate(startId: string, delta: SubtreeStats): void {
    let cur: string | null = startId;
    while (cur !== null) {
      const node = this.nodes.get(cur);
      if (!node) break;
      node.stats.nodeCount += delta.nodeCount;
      node.stats.weightSum += delta.weightSum;
      this.touched.add(cur);
      cur = node.parentId;
    }
  }

  /** Attach (or move) `childId` under `parentId`. Throws CYCLE atomically. */
  attach(childId: string, parentId: string): void {
    const child = this.get(childId);
    this.get(parentId);
    if (this.wouldCycle(childId, parentId)) {
      throw new GraphError(
        "CYCLE",
        `attaching ${childId} under ${parentId} would create a cycle`,
        "",
      );
    }
    if (child.parentId === parentId) {
      child.pendingParentId = null;
      this.orphans.delete(childId);
      return;
    }
    if (child.parentId !== null) this.detach(childId);
    this.get(parentId).children.add(childId);
    child.parentId = parentId;
    child.pendingParentId = null;
    this.orphans.delete(childId);
    this.propagate(parentId, child.stats);
  }

  /** Detach `childId` from its mounted parent, subtracting its subtree stats. */
  detach(childId: string): void {
    const child = this.get(childId);
    const p = child.parentId;
    if (p === null) return;
    this.get(p).children.delete(childId);
    child.parentId = null;
    this.propagate(p, neg(child.stats));
  }

  /** Mark `childId` as waiting for a not-yet-existing parent. */
  markOrphan(childId: string, pendingParentId: string): void {
    const child = this.get(childId);
    if (child.parentId !== null) this.detach(childId);
    child.pendingParentId = pendingParentId;
    this.orphans.set(childId, pendingParentId);
  }

  /** Clear any parent link (effective parent became null). */
  clearParent(childId: string): void {
    const child = this.get(childId);
    if (child.parentId !== null) this.detach(childId);
    child.pendingParentId = null;
    this.orphans.delete(childId);
  }

  /** Update own weight; delta propagates up the ancestor chain only. */
  setSelfWeight(id: string, weight: number): void {
    const node = this.get(id);
    const delta = weight - node.selfWeight;
    if (delta === 0) return;
    node.selfWeight = weight;
    this.propagate(id, { nodeCount: 0, weightSum: delta });
  }

  /**
   * Remove an entity. Explicit delete policy "orphan": children are kept with
   * their identity, detached, and queued waiting for this id to reappear.
   * Nothing is ever cascade-deleted silently.
   */
  removeEntity(id: string): string[] {
    const node = this.get(id);
    this.detach(id);
    const orphaned: string[] = [];
    for (const childId of [...node.children]) {
      this.detach(childId);
      const child = this.get(childId);
      child.pendingParentId = id;
      this.orphans.set(childId, id);
      orphaned.push(childId);
    }
    this.nodes.delete(id);
    return orphaned;
  }

  /**
   * Reattach orphans waiting for `parentId`. Returns attached ids.
   * A reattachment that would create a cycle is refused: the orphan stays
   * queued and the error is collected instead of corrupting the graph.
   */
  reattachOrphans(parentId: string, onError?: (e: GraphError) => void): string[] {
    const attached: string[] = [];
    for (const [orphanId, waitingFor] of [...this.orphans]) {
      if (waitingFor !== parentId) continue;
      try {
        this.attach(orphanId, parentId);
        attached.push(orphanId);
      } catch (e) {
        if (e instanceof GraphError && e.code === "CYCLE") {
          onError?.(e);
        } else {
          throw e;
        }
      }
    }
    return attached;
  }

  /** Ids whose stats changed since the last drain; clears the set. */
  drainTouched(): Set<string> {
    const out = new Set(this.touched);
    this.touched.clear();
    return out;
  }
}
