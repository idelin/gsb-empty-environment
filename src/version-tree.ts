import {
  BranchExistsError,
  UnknownBranchError,
  UnknownVersionError,
} from './errors.ts';

export interface VersionNode<S, E> {
  readonly id: string;
  readonly branch: string;
  readonly parentId: string | null;
  readonly seq: number;
  readonly event: E | null;
  readonly state: S;
  readonly createdAt: number;
}

export interface AppendResult<S, E> {
  readonly tree: VersionTree<S, E>;
  readonly node: VersionNode<S, E>;
}

/**
 * Persistent (immutable) version tree.
 *
 * Every mutating operation returns a NEW tree; previously obtained snapshots
 * remain valid and unchanged forever, which lets callers hold onto a
 * `store.versionTree` snapshot and diff/verify history at any later point.
 * Nodes themselves are never copied or mutated, so snapshots share structure.
 */
export class VersionTree<S, E> {
  readonly rootId: string;
  readonly size: number;

  private readonly nodes: ReadonlyMap<string, VersionNode<S, E>>;
  private readonly children: ReadonlyMap<string, readonly string[]>;
  private readonly tips: ReadonlyMap<string, string>;

  private constructor(
    rootId: string,
    size: number,
    nodes: ReadonlyMap<string, VersionNode<S, E>>,
    children: ReadonlyMap<string, readonly string[]>,
    tips: ReadonlyMap<string, string>,
  ) {
    this.rootId = rootId;
    this.size = size;
    this.nodes = nodes;
    this.children = children;
    this.tips = tips;
  }

  static create<S, E>(rootState: S, branch = 'main'): VersionTree<S, E> {
    const root: VersionNode<S, E> = {
      id: 'v1',
      branch,
      parentId: null,
      seq: 1,
      event: null,
      state: rootState,
      createdAt: Date.now(),
    };
    return new VersionTree(
      root.id,
      1,
      new Map([[root.id, root]]),
      new Map(),
      new Map([[branch, root.id]]),
    );
  }

  has(id: string): boolean {
    return this.nodes.has(id);
  }

  node(id: string): VersionNode<S, E> {
    const found = this.nodes.get(id);
    if (!found) throw new UnknownVersionError(id);
    return found;
  }

  hasBranch(name: string): boolean {
    return this.tips.has(name);
  }

  branches(): readonly string[] {
    return [...this.tips.keys()];
  }

  tipOf(branch: string): VersionNode<S, E> {
    const tipId = this.tips.get(branch);
    if (tipId === undefined) throw new UnknownBranchError(branch);
    return this.node(tipId);
  }

  childrenOf(id: string): readonly VersionNode<S, E>[] {
    if (!this.nodes.has(id)) throw new UnknownVersionError(id);
    return (this.children.get(id) ?? []).map((childId) => this.node(childId));
  }

  /** Path from the root down to (and including) `id`. */
  pathTo(id: string): readonly VersionNode<S, E>[] {
    const path: VersionNode<S, E>[] = [];
    let cursor: VersionNode<S, E> = this.node(id);
    for (;;) {
      path.unshift(cursor);
      if (cursor.parentId === null) return path;
      cursor = this.node(cursor.parentId);
    }
  }

  isAncestor(ancestorId: string, descendantId: string): boolean {
    let cursor: VersionNode<S, E> = this.node(descendantId);
    for (;;) {
      if (cursor.id === ancestorId) return true;
      if (cursor.parentId === null) return false;
      cursor = this.node(cursor.parentId);
    }
  }

  /** Append a new version on top of `parentId`, labelled with `branch`. */
  append(
    parentId: string,
    branch: string,
    event: E,
    state: S,
  ): AppendResult<S, E> {
    const parent = this.node(parentId);
    const node: VersionNode<S, E> = {
      id: `v${this.size + 1}`,
      branch,
      parentId: parent.id,
      seq: this.size + 1,
      event,
      state,
      createdAt: Date.now(),
    };

    const nodes = new Map(this.nodes);
    nodes.set(node.id, node);

    const children = new Map(this.children);
    children.set(parent.id, [...(children.get(parent.id) ?? []), node.id]);

    const tips = new Map(this.tips);
    tips.set(branch, node.id);

    return {
      tree: new VersionTree(this.rootId, this.size + 1, nodes, children, tips),
      node,
    };
  }

  /** Create a new branch pointer at an existing version. Never moves other tips. */
  forkBranch(name: string, fromVersionId: string): VersionTree<S, E> {
    if (this.tips.has(name)) throw new BranchExistsError(name);
    this.node(fromVersionId);
    const tips = new Map(this.tips);
    tips.set(name, fromVersionId);
    return new VersionTree(this.rootId, this.size, this.nodes, this.children, tips);
  }
}
