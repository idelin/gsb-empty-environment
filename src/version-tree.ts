/**
 * Persistent (immutable) version tree.
 *
 * Every mutating operation returns a new tree that structurally shares the
 * previous one's data, so old references stay valid forever. The tree is the
 * single source of truth for branches: a branch is just a named pointer to a
 * node, and appending moves exactly one branch pointer forward.
 */

export interface VersionNode<S> {
  readonly id: string;
  readonly branch: string;
  readonly parentId: string | null;
  readonly event: string;
  readonly state: S;
  readonly createdAt: number;
}

export interface VersionTreeJSON<S> {
  readonly nodes: ReadonlyArray<VersionNode<S>>;
  readonly branches: Record<string, string>;
}

export class VersionTree<S> {
  private readonly nodes: ReadonlyMap<string, VersionNode<S>>;
  private readonly childIds: ReadonlyMap<string, ReadonlyArray<string>>;
  private readonly branchHeads: ReadonlyMap<string, string>;

  private constructor(
    nodes: ReadonlyMap<string, VersionNode<S>>,
    childIds: ReadonlyMap<string, ReadonlyArray<string>>,
    branchHeads: ReadonlyMap<string, string>,
  ) {
    this.nodes = nodes;
    this.childIds = childIds;
    this.branchHeads = branchHeads;
  }

  static empty<S>(): VersionTree<S> {
    return new VersionTree<S>(new Map(), new Map(), new Map());
  }

  get size(): number {
    return this.nodes.size;
  }

  hasNode(id: string): boolean {
    return this.nodes.has(id);
  }

  getNode(id: string): VersionNode<S> {
    const node = this.nodes.get(id);
    if (!node) {
      throw new Error(`Unknown version "${id}"`);
    }
    return node;
  }

  findNode(id: string): VersionNode<S> | undefined {
    return this.nodes.get(id);
  }

  hasBranch(name: string): boolean {
    return this.branchHeads.has(name);
  }

  getBranchHeadId(name: string): string {
    const id = this.branchHeads.get(name);
    if (id === undefined) {
      throw new Error(`Unknown branch "${name}"`);
    }
    return id;
  }

  findBranchHeadId(name: string): string | undefined {
    return this.branchHeads.get(name);
  }

  getBranchHead(name: string): VersionNode<S> {
    return this.getNode(this.getBranchHeadId(name));
  }

  listBranches(): string[] {
    return Array.from(this.branchHeads.keys());
  }

  getChildren(id: string): VersionNode<S>[] {
    const ids = this.childIds.get(id) ?? [];
    return ids.map((childId) => this.getNode(childId));
  }

  isBranchTip(id: string): boolean {
    for (const headId of this.branchHeads.values()) {
      if (headId === id) {
        return true;
      }
    }
    return false;
  }

  /** Path from the root down to the given node (inclusive), oldest first. */
  pathFromRoot(id: string): VersionNode<S>[] {
    const path: VersionNode<S>[] = [];
    let cursor: VersionNode<S> | undefined = this.getNode(id);
    while (cursor) {
      path.unshift(cursor);
      cursor = cursor.parentId === null ? undefined : this.nodes.get(cursor.parentId);
    }
    return path;
  }

  /**
   * Append a new node on `branch` with `parentId` as its parent, moving that
   * branch's head pointer to the new node. Returns the new tree and node.
   */
  append(input: {
    parentId: string | null;
    branch: string;
    event: string;
    state: S;
    id?: string;
    createdAt?: number;
  }): { tree: VersionTree<S>; node: VersionNode<S> } {
    const { parentId, branch, event, state } = input;
    if (parentId !== null && !this.nodes.has(parentId)) {
      throw new Error(`Cannot append: unknown parent version "${parentId}"`);
    }
    const id = input.id ?? `v${this.nodes.size + 1}`;
    if (this.nodes.has(id)) {
      throw new Error(`Duplicate version id "${id}"`);
    }
    const node: VersionNode<S> = {
      id,
      branch,
      parentId,
      event,
      state,
      createdAt: input.createdAt ?? Date.now(),
    };
    const nodes = new Map(this.nodes);
    nodes.set(id, node);
    const childIds = new Map(this.childIds);
    if (parentId !== null) {
      childIds.set(parentId, [...(childIds.get(parentId) ?? []), id]);
    }
    const branchHeads = new Map(this.branchHeads);
    branchHeads.set(branch, id);
    return { tree: new VersionTree<S>(nodes, childIds, branchHeads), node };
  }

  /**
   * Create a named branch pointer at an existing node. Never moves or
   * overwrites any existing branch.
   */
  createBranch(name: string, fromId: string): VersionTree<S> {
    if (this.branchHeads.has(name)) {
      throw new Error(`Branch "${name}" already exists`);
    }
    if (!this.nodes.has(fromId)) {
      throw new Error(`Cannot branch: unknown version "${fromId}"`);
    }
    const branchHeads = new Map(this.branchHeads);
    branchHeads.set(name, fromId);
    return new VersionTree<S>(this.nodes, this.childIds, branchHeads);
  }

  toJSON(): VersionTreeJSON<S> {
    return {
      nodes: Array.from(this.nodes.values()),
      branches: Object.fromEntries(this.branchHeads),
    };
  }

  static fromJSON<S>(json: VersionTreeJSON<S>): VersionTree<S> {
    const nodes = new Map<string, VersionNode<S>>();
    const childIds = new Map<string, string[]>();
    for (const node of json.nodes) {
      if (nodes.has(node.id)) {
        throw new Error(`Duplicate version id "${node.id}" in snapshot`);
      }
      nodes.set(node.id, node);
      if (node.parentId !== null) {
        const siblings = childIds.get(node.parentId) ?? [];
        siblings.push(node.id);
        childIds.set(node.parentId, siblings);
      }
    }
    const branchHeads = new Map<string, string>(Object.entries(json.branches));
    for (const [name, headId] of branchHeads) {
      if (!nodes.has(headId)) {
        throw new Error(`Branch "${name}" points at unknown version "${headId}"`);
      }
    }
    return new VersionTree<S>(nodes, childIds, branchHeads);
  }
}
