import {
  DetachedHeadError,
  UnknownBranchError,
  UnknownVersionError,
  ViewClosedError,
} from './errors.ts';
import { VersionTree } from './version-tree.ts';
import type { VersionNode } from './version-tree.ts';

export type Reducer<S, E> = (state: S, event: E) => S;
export type Unsubscribe = () => void;
export type BranchListener<S, E> = (
  node: VersionNode<S, E>,
  branch: string,
) => void;
export type ChildListener<S, E> = (child: VersionNode<S, E>) => void;

/**
 * A read-only session pinned to a historical version.
 *
 * Opening a view never moves the store's real head: new events keep
 * appending to the active branch while the view is open. A view cannot
 * commit — editing the past requires an explicit `fork(name)`, which
 * creates a new branch instead of overwriting an existing one.
 *
 * Views own resources (child subscriptions). A view is closed by
 * `close()`, by `fork()`, or implicitly when the store checks out another
 * version; closing releases every subscription it registered.
 */
export class ViewSession<S, E> {
  readonly versionId: string;

  private store: HistoryStore<S, E> | null;
  private readonly cleanups: Unsubscribe[] = [];

  constructor(store: HistoryStore<S, E>, versionId: string) {
    this.store = store;
    this.versionId = versionId;
  }

  get closed(): boolean {
    return this.store === null;
  }

  get version(): VersionNode<S, E> {
    return this.requireStore().versionTree.node(this.versionId);
  }

  get state(): S {
    return this.version.state;
  }

  get branch(): string {
    return this.version.branch;
  }

  /** True when the viewed version is the tip of its own branch. */
  get isHead(): boolean {
    const store = this.requireStore();
    return store.versionTree.tipOf(this.branch).id === this.versionId;
  }

  /** Historical views are detached: committing is always refused. */
  commit(_event: E): never {
    throw new DetachedHeadError(this.versionId);
  }

  /**
   * Explicitly create a branch at this version and make it the store's
   * active branch. The source branch (e.g. main) is left untouched.
   * Closes this view.
   */
  fork(branchName: string): VersionNode<S, E> {
    const store = this.requireStore();
    store.createBranch(branchName, this.versionId);
    store.switchBranch(branchName);
    this.close();
    return store.head;
  }

  /**
   * Subscribe to children appended directly on top of the viewed version
   * (e.g. live commits while viewing the head, or someone forking here).
   * The subscription is released automatically when the view closes.
   */
  onChild(listener: ChildListener<S, E>): Unsubscribe {
    const store = this.requireStore();
    const unsubscribe = store.subscribeChildren(this.versionId, listener);
    this.cleanups.push(unsubscribe);
    return () => {
      unsubscribe();
      const index = this.cleanups.indexOf(unsubscribe);
      if (index >= 0) this.cleanups.splice(index, 1);
    };
  }

  close(): void {
    if (this.store === null) return;
    const store = this.store;
    this.store = null;
    for (const cleanup of this.cleanups.splice(0)) cleanup();
    store.releaseView(this);
  }

  private requireStore(): HistoryStore<S, E> {
    if (this.store === null) throw new ViewClosedError();
    return this.store;
  }
}

/**
 * Append-only state history over a persistent version tree.
 *
 * - `commit` always appends to the tip of the active branch (the real head).
 * - `checkout` opens a read-only view of any version without moving the head.
 * - Branches are created explicitly (`createBranch` / `view.fork`) and can
 *   never overwrite an existing branch.
 */
export class HistoryStore<S, E> {
  private tree: VersionTree<S, E>;
  private activeBranch: string;
  private activeView: ViewSession<S, E> | null = null;
  private readonly reducer: Reducer<S, E>;
  private readonly childListeners = new Map<string, Set<ChildListener<S, E>>>();
  private readonly branchListeners = new Map<string, Set<BranchListener<S, E>>>();

  constructor(reducer: Reducer<S, E>, initialState: S, mainBranch = 'main') {
    this.reducer = reducer;
    this.activeBranch = mainBranch;
    this.tree = VersionTree.create(initialState, mainBranch);
  }

  /** Immutable snapshot of the full version tree at this moment. */
  get versionTree(): VersionTree<S, E> {
    return this.tree;
  }

  /** The real latest head: tip of the active branch. */
  get head(): VersionNode<S, E> {
    return this.tree.tipOf(this.activeBranch);
  }

  get activeBranchName(): string {
    return this.activeBranch;
  }

  get activeViewSession(): ViewSession<S, E> | null {
    return this.activeView;
  }

  /** Total live subscriptions (view-scoped + branch-scoped). */
  get subscriptionCount(): number {
    let count = 0;
    for (const set of this.childListeners.values()) count += set.size;
    for (const set of this.branchListeners.values()) count += set.size;
    return count;
  }

  listBranches(): readonly { name: string; tipId: string }[] {
    return this.tree
      .branches()
      .map((name) => ({ name, tipId: this.tree.tipOf(name).id }));
  }

  /** Append an event to the real head of the active branch. */
  commit(event: E): VersionNode<S, E> {
    const tip = this.tree.tipOf(this.activeBranch);
    const state = this.reducer(tip.state, event);
    const { tree, node } = this.tree.append(tip.id, this.activeBranch, event, state);
    this.tree = tree;
    this.emitChild(tip.id, node);
    this.emitBranch(this.activeBranch, node);
    return node;
  }

  /**
   * Open a read-only view of any version. Does not move the head and does
   * not change the active branch. Closes the previously opened view, if any.
   */
  checkout(versionId: string): ViewSession<S, E> {
    if (!this.tree.has(versionId)) throw new UnknownVersionError(versionId);
    this.activeView?.close();
    const view = new ViewSession(this, versionId);
    this.activeView = view;
    return view;
  }

  /** Create a new branch pointing at an existing version. */
  createBranch(name: string, fromVersionId: string): void {
    this.tree = this.tree.forkBranch(name, fromVersionId);
  }

  /** Switch the active branch; future commits land on its tip. */
  switchBranch(name: string): void {
    if (!this.tree.hasBranch(name)) throw new UnknownBranchError(name);
    this.activeBranch = name;
  }

  /** Subscribe to commits landing on a branch. Manual cleanup via returned fn. */
  subscribeBranch(branch: string, listener: BranchListener<S, E>): Unsubscribe {
    if (!this.tree.hasBranch(branch)) throw new UnknownBranchError(branch);
    let set = this.branchListeners.get(branch);
    if (!set) {
      set = new Set();
      this.branchListeners.set(branch, set);
    }
    set.add(listener);
    return () => {
      set.delete(listener);
      if (set.size === 0) this.branchListeners.delete(branch);
    };
  }

  /** Internal: used by ViewSession.onChild. */
  subscribeChildren(versionId: string, listener: ChildListener<S, E>): Unsubscribe {
    if (!this.tree.has(versionId)) throw new UnknownVersionError(versionId);
    let set = this.childListeners.get(versionId);
    if (!set) {
      set = new Set();
      this.childListeners.set(versionId, set);
    }
    set.add(listener);
    return () => {
      set.delete(listener);
      if (set.size === 0) this.childListeners.delete(versionId);
    };
  }

  /** Internal: detach a closed view if it is still the active one. */
  releaseView(view: ViewSession<S, E>): void {
    if (this.activeView === view) this.activeView = null;
  }

  private emitChild(parentId: string, node: VersionNode<S, E>): void {
    const set = this.childListeners.get(parentId);
    if (!set) return;
    for (const listener of [...set]) listener(node);
  }

  private emitBranch(branch: string, node: VersionNode<S, E>): void {
    const set = this.branchListeners.get(branch);
    if (!set) return;
    for (const listener of [...set]) listener(node, branch);
  }
}
