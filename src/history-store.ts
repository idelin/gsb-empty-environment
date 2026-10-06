import { VersionNode, VersionTree, VersionTreeJSON } from "./version-tree";

export const MAIN_BRANCH = "main";

export interface ViewInfo<S> {
  /** Version currently shown to the user. */
  readonly versionId: string;
  /** Branch the viewed version belongs to. */
  readonly versionBranch: string;
  /** Branch that receives new events (the real head lives here). */
  readonly activeBranch: string;
  /** True when the view sits on the real head of the active branch. */
  readonly isHead: boolean;
  readonly event: string;
  readonly state: S;
}

export interface BranchInfo {
  readonly name: string;
  readonly headId: string;
  readonly isActive: boolean;
}

export interface HistoryStoreOptions<S> {
  readonly initialState: S;
  readonly initialEvent?: string;
  readonly mainBranch?: string;
  readonly now?: () => number;
}

export interface HistoryStoreSnapshot<S> {
  readonly tree: VersionTreeJSON<S>;
  readonly activeBranch: string;
  readonly viewId: string;
}

export type HeadListener<S> = (node: VersionNode<S>, branch: string) => void;
export type ViewListener<S> = (view: ViewInfo<S>) => void;
/**
 * A view-scoped resource. Called with the current view whenever the viewed
 * version changes. May return a cleanup function, which is guaranteed to run
 * before the resource is set up for the next view (and when released).
 */
export type ViewResource<S> = (view: ViewInfo<S>) => void | (() => void);

/**
 * State history store with time-travel viewing and explicit branching.
 *
 * Invariants:
 * - `checkout` only moves the view cursor; the real head never moves.
 * - `dispatch` always appends to the real head of the active branch, even
 *   while an old version is being viewed.
 * - Editing an old version via `editFrom` always creates a new branch; it
 *   can never overwrite `main` or any other existing branch.
 */
export class HistoryStore<S> {
  private tree: VersionTree<S>;
  private activeBranchName: string;
  private viewVersionId: string;
  private readonly now: () => number;
  private readonly headListeners: Set<HeadListener<S>>;
  private readonly viewListeners: Set<ViewListener<S>>;
  private readonly resources: Set<ViewResource<S>>;
  private readonly resourceCleanups: Map<ViewResource<S>, () => void>;

  private constructor(
    tree: VersionTree<S>,
    activeBranch: string,
    viewId: string,
    now: () => number,
  ) {
    this.tree = tree;
    this.activeBranchName = activeBranch;
    this.viewVersionId = viewId;
    this.now = now;
    this.headListeners = new Set();
    this.viewListeners = new Set();
    this.resources = new Set();
    this.resourceCleanups = new Map();
  }

  static create<S>(options: HistoryStoreOptions<S>): HistoryStore<S> {
    const now = options.now ?? (() => Date.now());
    const main = options.mainBranch ?? MAIN_BRANCH;
    const { tree, node } = VersionTree.empty<S>().append({
      parentId: null,
      branch: main,
      event: options.initialEvent ?? "init",
      state: options.initialState,
      createdAt: now(),
    });
    return new HistoryStore<S>(tree, main, node.id, now);
  }

  static restore<S>(
    snapshot: HistoryStoreSnapshot<S>,
    options?: { now?: () => number },
  ): HistoryStore<S> {
    const tree = VersionTree.fromJSON(snapshot.tree);
    if (!tree.hasBranch(snapshot.activeBranch)) {
      throw new Error(`Snapshot active branch "${snapshot.activeBranch}" does not exist`);
    }
    if (!tree.hasNode(snapshot.viewId)) {
      throw new Error(`Snapshot view version "${snapshot.viewId}" does not exist`);
    }
    const now = options?.now ?? (() => Date.now());
    return new HistoryStore<S>(tree, snapshot.activeBranch, snapshot.viewId, now);
  }

  /** The persistent version tree backing this store (immutable snapshot). */
  get versionTree(): VersionTree<S> {
    return this.tree;
  }

  get activeBranch(): string {
    return this.activeBranchName;
  }

  /** Real latest head of the active branch. Viewing never changes this. */
  get headId(): string {
    return this.tree.getBranchHeadId(this.activeBranchName);
  }

  /** Version currently being viewed. */
  get viewId(): string {
    return this.viewVersionId;
  }

  get isViewingHead(): boolean {
    return this.viewVersionId === this.headId;
  }

  getHeadState(): S {
    return this.tree.getNode(this.headId).state;
  }

  getView(): ViewInfo<S> {
    const node = this.tree.getNode(this.viewVersionId);
    return {
      versionId: node.id,
      versionBranch: node.branch,
      activeBranch: this.activeBranchName,
      isHead: node.id === this.headId,
      event: node.event,
      state: node.state,
    };
  }

  listBranches(): BranchInfo[] {
    return this.tree.listBranches().map((name) => ({
      name,
      headId: this.tree.getBranchHeadId(name),
      isActive: name === this.activeBranchName,
    }));
  }

  /**
   * Append an event to the real head of the active branch. Works regardless
   * of which version is being viewed. When the view sits on the head it
   * follows the head forward; a detached view (old version) stays put.
   */
  dispatch(event: string, state: S): VersionNode<S> {
    const viewFollowsHead = this.isViewingHead;
    const { tree, node } = this.tree.append({
      parentId: this.headId,
      branch: this.activeBranchName,
      event,
      state,
      createdAt: this.now(),
    });
    this.tree = tree;
    if (viewFollowsHead) {
      this.viewVersionId = node.id;
      this.runViewChange();
    }
    for (const listener of this.headListeners) {
      listener(node, this.activeBranchName);
    }
    return node;
  }

  /**
   * View-only navigation to any version. The real head is untouched, so new
   * events keep landing on the active branch while the user looks around.
   */
  checkout(versionId: string): ViewInfo<S> {
    this.tree.getNode(versionId);
    if (versionId === this.viewVersionId) {
      return this.getView();
    }
    this.viewVersionId = versionId;
    this.runViewChange();
    return this.getView();
  }

  /** Move the view back to the real head of the active branch. */
  backToHead(): ViewInfo<S> {
    return this.checkout(this.headId);
  }

  /**
   * Edit starting from a specific version.
   *
   * If `versionId` is the real head of the active branch this is a plain
   * dispatch. Otherwise a new branch is explicitly created at that version
   * and the edit becomes its first node; the source branch (including
   * `main`) is never overwritten. Throws if `branchName` already exists.
   */
  editFrom(
    versionId: string,
    event: string,
    state: S,
    branchName?: string,
  ): { branch: string; node: VersionNode<S> } {
    this.tree.getNode(versionId);
    if (versionId === this.headId) {
      return { branch: this.activeBranchName, node: this.dispatch(event, state) };
    }
    const name = branchName ?? this.nextBranchName(versionId);
    if (this.tree.hasBranch(name)) {
      throw new Error(
        `Branch "${name}" already exists; editing an old version must not overwrite it`,
      );
    }
    this.tree = this.tree.createBranch(name, versionId);
    this.activeBranchName = name;
    const { tree, node } = this.tree.append({
      parentId: versionId,
      branch: name,
      event,
      state,
      createdAt: this.now(),
    });
    this.tree = tree;
    this.viewVersionId = node.id;
    this.runViewChange();
    for (const listener of this.headListeners) {
      listener(node, name);
    }
    return { branch: name, node };
  }

  /**
   * Switch the active branch. The view resets to that branch's head and
   * view-scoped resources are cleaned up and re-created.
   */
  switchBranch(name: string): ViewInfo<S> {
    const headId = this.tree.getBranchHeadId(name);
    const viewChanged = name !== this.activeBranchName || this.viewVersionId !== headId;
    this.activeBranchName = name;
    this.viewVersionId = headId;
    if (viewChanged) {
      this.runViewChange();
    }
    return this.getView();
  }

  /** Subscribe to new events appended to the real head. */
  subscribe(listener: HeadListener<S>): () => void {
    this.headListeners.add(listener);
    return () => {
      this.headListeners.delete(listener);
    };
  }

  /** Subscribe to view-cursor movements (checkout / branch switch / edit). */
  subscribeView(listener: ViewListener<S>): () => void {
    this.viewListeners.add(listener);
    return () => {
      this.viewListeners.delete(listener);
    };
  }

  /**
   * Attach a view-scoped resource. Its cleanup runs whenever the view moves
   * away (checkout, branch switch, branch-creating edit) and when the
   * returned release function is called.
   */
  watchView(resource: ViewResource<S>): () => void {
    this.resources.add(resource);
    const cleanup = resource(this.getView());
    if (typeof cleanup === "function") {
      this.resourceCleanups.set(resource, cleanup);
    }
    return () => {
      this.resources.delete(resource);
      const stored = this.resourceCleanups.get(resource);
      if (stored) {
        this.resourceCleanups.delete(resource);
        stored();
      }
    };
  }

  snapshot(): HistoryStoreSnapshot<S> {
    return {
      tree: this.tree.toJSON(),
      activeBranch: this.activeBranchName,
      viewId: this.viewVersionId,
    };
  }

  private runViewChange(): void {
    for (const cleanup of this.resourceCleanups.values()) {
      cleanup();
    }
    this.resourceCleanups.clear();
    const view = this.getView();
    for (const resource of this.resources) {
      const cleanup = resource(view);
      if (typeof cleanup === "function") {
        this.resourceCleanups.set(resource, cleanup);
      }
    }
    for (const listener of this.viewListeners) {
      listener(view);
    }
  }

  private nextBranchName(versionId: string): string {
    let candidate = `branch-${versionId}`;
    let counter = 2;
    while (this.tree.hasBranch(candidate)) {
      candidate = `branch-${versionId}-${counter}`;
      counter += 1;
    }
    return candidate;
  }
}
