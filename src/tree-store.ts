export interface TreeNodeData {
  id: string;
  hasChildren?: boolean;
}

export type ChildrenLoader = (parentId: string | null) => Promise<TreeNodeData[]>;

export type LoadStatus = "idle" | "loading" | "loaded" | "error";

export interface TreeNode {
  readonly id: string;
  readonly parentId: string | null;
  childIds: string[];
  hasChildren: boolean;
  expanded: boolean;
  status: LoadStatus;
  generation: number;
  error: unknown;
}

export const ROOT_ID = "__root__";

interface Inflight {
  generation: number;
  promise: Promise<void>;
}

function createNode(id: string, parentId: string | null, hasChildren: boolean): TreeNode {
  return {
    id,
    parentId,
    childIds: [],
    hasChildren,
    expanded: false,
    status: "idle",
    generation: 0,
    error: null,
  };
}

export class TreeStore {
  private readonly loader: ChildrenLoader;
  private readonly nodes = new Map<string, TreeNode>();
  private readonly inflight = new Map<string, Inflight>();
  private selected: string | null = null;

  constructor(loader: ChildrenLoader) {
    this.loader = loader;
    const root = createNode(ROOT_ID, null, true);
    root.expanded = true;
    this.nodes.set(ROOT_ID, root);
  }

  get(id: string): TreeNode | undefined {
    return this.nodes.get(id);
  }

  rootIds(): string[] {
    return [...this.mustGet(ROOT_ID).childIds];
  }

  childrenOf(id: string): TreeNode[] {
    return this.mustGet(id).childIds.map((childId) => this.mustGet(childId));
  }

  selectedId(): string | null {
    return this.selected;
  }

  selectionPath(): string[] {
    const path: string[] = [];
    let cursor = this.selected;
    while (cursor !== null && cursor !== ROOT_ID) {
      const node = this.nodes.get(cursor);
      if (!node) break;
      path.unshift(node.id);
      cursor = node.parentId;
    }
    return path;
  }

  select(id: string | null): void {
    if (id !== null && !this.nodes.has(id)) {
      throw new Error(`cannot select unknown node: ${id}`);
    }
    this.selected = id;
  }

  expand(id: string): Promise<void> {
    const node = this.mustGet(id);
    node.expanded = true;
    const flight = this.inflight.get(id);
    if (flight && flight.generation === node.generation) {
      return flight.promise;
    }
    if (node.status === "loaded") {
      return Promise.resolve();
    }
    return this.startLoad(node);
  }

  refresh(id: string): Promise<void> {
    const node = this.mustGet(id);
    node.generation += 1;
    node.expanded = true;
    return this.startLoad(node);
  }

  collapse(id: string): void {
    const node = this.mustGet(id);
    if (id === ROOT_ID) {
      throw new Error("cannot collapse the root");
    }
    node.expanded = false;
    node.generation += 1;
    node.status = node.status === "loaded" ? "loaded" : "idle";
    node.error = null;
    this.inflight.delete(id);
  }

  remove(id: string): void {
    if (id === ROOT_ID) {
      throw new Error("cannot remove the root");
    }
    const node = this.nodes.get(id);
    if (!node) return;
    const parentId = node.parentId;
    this.removeSubtree(id);
    const parent = parentId === null ? undefined : this.nodes.get(parentId);
    if (parent) {
      parent.childIds = parent.childIds.filter((childId) => childId !== id);
    }
    if (this.selected !== null && !this.nodes.has(this.selected)) {
      this.selected =
        parentId !== null && parentId !== ROOT_ID && this.nodes.has(parentId)
          ? parentId
          : null;
    }
  }

  private mustGet(id: string): TreeNode {
    const node = this.nodes.get(id);
    if (!node) {
      throw new Error(`unknown node: ${id}`);
    }
    return node;
  }

  private removeSubtree(id: string): void {
    const stack = [id];
    while (stack.length > 0) {
      const currentId = stack.pop() as string;
      const record = this.nodes.get(currentId);
      if (!record) continue;
      stack.push(...record.childIds);
      this.nodes.delete(currentId);
      this.inflight.delete(currentId);
    }
  }

  private startLoad(node: TreeNode): Promise<void> {
    const id = node.id;
    const generation = node.generation;
    node.status = "loading";
    node.error = null;
    const promise = this.loader(id === ROOT_ID ? null : id).then(
      (children) => {
        const current = this.nodes.get(id);
        if (!current || current.generation !== generation) return;
        this.applyChildren(current, children);
        current.status = "loaded";
        current.error = null;
      },
      (error: unknown) => {
        const current = this.nodes.get(id);
        if (!current || current.generation !== generation) return;
        current.status = "error";
        current.error = error;
      },
    ).finally(() => {
      const flight = this.inflight.get(id);
      if (flight && flight.generation === generation) {
        this.inflight.delete(id);
      }
    });
    this.inflight.set(id, { generation, promise });
    return promise;
  }

  private applyChildren(node: TreeNode, children: TreeNodeData[]): void {
    const nextIds: string[] = [];
    const seen = new Set<string>();
    for (const child of children) {
      if (seen.has(child.id)) continue;
      seen.add(child.id);
      nextIds.push(child.id);
      const existing = this.nodes.get(child.id);
      if (existing) {
        existing.hasChildren = child.hasChildren ?? existing.hasChildren;
      } else {
        this.nodes.set(child.id, createNode(child.id, node.id, child.hasChildren ?? false));
      }
    }
    for (const previousId of node.childIds) {
      if (!seen.has(previousId)) {
        this.removeSubtree(previousId);
      }
    }
    node.childIds = nextIds;
    if (this.selected !== null && !this.nodes.has(this.selected)) {
      this.selected = node.id === ROOT_ID ? null : node.id;
    }
  }
}
