export type NodeId = string;

export type LoadStatus = "idle" | "loading" | "loaded" | "error";

export interface TreeNodeState {
  readonly id: NodeId;
  parentId: NodeId | null;
  childIds: NodeId[];
  expanded: boolean;
  status: LoadStatus;
  /** Bumped on collapse; responses captured under an older generation are discarded. */
  generation: number;
  error: unknown;
}

export interface ChildData {
  id: NodeId;
}

export type Loader = (id: NodeId) => Promise<readonly ChildData[]>;

export interface TreeStoreOptions {
  loader: Loader;
  rootId?: NodeId;
}

interface InflightRecord {
  readonly generation: number;
}

function createNode(id: NodeId, parentId: NodeId | null): TreeNodeState {
  return {
    id,
    parentId,
    childIds: [],
    expanded: false,
    status: "idle",
    generation: 0,
    error: null,
  };
}

/**
 * Async tree state store.
 *
 * Concurrency contract:
 * - Every load is tagged with the node's current generation. Collapsing a node
 *   bumps its generation, so a response that was requested before the collapse
 *   is discarded when it arrives late.
 * - Within one generation at most one request per node is in flight; repeated
 *   expand/refresh calls share it (the loader is invoked exactly once).
 * - Deleting a node removes its whole subtree synchronously; late responses
 *   addressed to any removed id find no node and are dropped, so deleted
 *   nodes never reappear ("ghost nodes").
 * - A failed load keeps the children that are already displayed; `retry`
 *   issues a new request for the same generation.
 */
export class TreeStore {
  readonly rootId: NodeId;
  private readonly loader: Loader;
  private readonly nodes = new Map<NodeId, TreeNodeState>();
  private readonly inflight = new Map<NodeId, InflightRecord>();
  private selectedId: NodeId | null = null;
  private loadCount = 0;

  constructor(options: TreeStoreOptions) {
    this.loader = options.loader;
    this.rootId = options.rootId ?? "root";
    this.nodes.set(this.rootId, createNode(this.rootId, null));
  }

  /** Total number of times the loader has been invoked. */
  get loads(): number {
    return this.loadCount;
  }

  has(id: NodeId): boolean {
    return this.nodes.has(id);
  }

  getNode(id: NodeId): Readonly<TreeNodeState> | undefined {
    return this.nodes.get(id);
  }

  getChildIds(id: NodeId): readonly NodeId[] {
    return this.mustGet(id).childIds;
  }

  isLoading(id: NodeId): boolean {
    return this.inflight.has(id);
  }

  expand(id: NodeId): void {
    const node = this.mustGet(id);
    node.expanded = true;
    if (node.status === "idle") {
      this.startLoad(node);
    }
    // "loading": share the in-flight request of this generation.
    // "loaded": reuse cached children. "error": wait for an explicit retry().
  }

  collapse(id: NodeId): void {
    const node = this.mustGet(id);
    node.expanded = false;
    node.generation += 1;
    if (node.status === "loading") {
      node.status = node.childIds.length > 0 ? "loaded" : "idle";
    }
  }

  /** Reload a node that already has children; keeps them visible meanwhile. */
  refresh(id: NodeId): void {
    const node = this.mustGet(id);
    if (node.status === "loading") return;
    this.startLoad(node);
  }

  retry(id: NodeId): void {
    const node = this.mustGet(id);
    if (node.status !== "error") return;
    this.startLoad(node);
  }

  remove(id: NodeId): void {
    const node = this.nodes.get(id);
    if (!node) return;
    if (node.parentId !== null) {
      const parent = this.nodes.get(node.parentId);
      if (parent) {
        parent.childIds = parent.childIds.filter((childId) => childId !== id);
      }
    }
    const removed: NodeId[] = [];
    const stack: NodeId[] = [id];
    while (stack.length > 0) {
      const current = stack.pop() as NodeId;
      const target = this.nodes.get(current);
      if (!target) continue;
      removed.push(current);
      stack.push(...target.childIds);
      this.nodes.delete(current);
      this.inflight.delete(current);
    }
    if (this.selectedId !== null && removed.includes(this.selectedId)) {
      this.selectedId = node.parentId;
    }
  }

  select(id: NodeId | null): void {
    if (id !== null) this.mustGet(id);
    this.selectedId = id;
  }

  get selected(): NodeId | null {
    return this.selectedId;
  }

  /** Ids from the root down to the selected node; empty when nothing selected. */
  getSelectionPath(): NodeId[] {
    const path: NodeId[] = [];
    let current = this.selectedId;
    while (current !== null) {
      path.unshift(current);
      current = this.nodes.get(current)?.parentId ?? null;
    }
    return path;
  }

  private mustGet(id: NodeId): TreeNodeState {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`Unknown tree node: ${id}`);
    return node;
  }

  private startLoad(node: TreeNodeState): void {
    const generation = node.generation;
    const existing = this.inflight.get(node.id);
    if (existing && existing.generation === generation) return;
    const record: InflightRecord = { generation };
    this.inflight.set(node.id, record);
    node.status = "loading";
    node.error = null;
    this.loadCount += 1;
    this.loader(node.id).then(
      (children) => this.onResolve(node.id, record, children),
      (error) => this.onReject(node.id, record, error),
    );
  }

  private onResolve(
    id: NodeId,
    record: InflightRecord,
    children: readonly ChildData[],
  ): void {
    if (this.inflight.get(id) === record) this.inflight.delete(id);
    const node = this.nodes.get(id);
    if (!node) return; // deleted while loading: drop, never resurrect
    if (node.generation !== record.generation) return; // stale generation
    const childIds: NodeId[] = [];
    for (const child of children) {
      childIds.push(child.id);
      const existing = this.nodes.get(child.id);
      if (existing) {
        if (existing.parentId !== null && existing.parentId !== id) {
          const oldParent = this.nodes.get(existing.parentId);
          if (oldParent) {
            oldParent.childIds = oldParent.childIds.filter(
              (childId) => childId !== child.id,
            );
          }
        }
        existing.parentId = id; // keep the child's subtree state intact
      } else {
        this.nodes.set(child.id, createNode(child.id, id));
      }
    }
    node.childIds = childIds;
    node.status = "loaded";
  }

  private onReject(id: NodeId, record: InflightRecord, error: unknown): void {
    if (this.inflight.get(id) === record) this.inflight.delete(id);
    const node = this.nodes.get(id);
    if (!node) return;
    if (node.generation !== record.generation) return;
    node.status = "error"; // childIds untouched: the displayed tree survives
    node.error = error;
  }
}
