import { VirtualHost } from "./host.ts";
import type { HostNode } from "./host.ts";
import { Subscription, ResourceTracker } from "./resources.ts";

export interface TreeSpec {
  type: string;
  label?: string;
  children?: TreeSpec[];
}

interface RenderedNode {
  spec: TreeSpec;
  hostNode: HostNode;
  sub: Subscription;
  children: RenderedNode[];
}

function disposeTree(node: RenderedNode): void {
  node.sub.dispose();
  for (const child of node.children) disposeTree(child);
}

/**
 * Prepare phase: incrementally materializes a *detached* tree, one node
 * per step. Acquiring a node's subscription is the observable side effect.
 * A session can be paused (simply not stepped) or aborted when superseded;
 * abort disposes every subscription it acquired so far.
 */
class PrepareSession {
  readonly version: number;
  readonly nodes: RenderedNode[] = [];
  root: RenderedNode | null = null;
  aborted = false;
  private readonly pending: Array<{ spec: TreeSpec; parent: RenderedNode | null }> = [];
  private readonly tracker: ResourceTracker;
  private readonly host: VirtualHost;

  constructor(version: number, spec: TreeSpec, tracker: ResourceTracker, host: VirtualHost) {
    this.version = version;
    this.tracker = tracker;
    this.host = host;
    this.pending.push({ spec, parent: null });
  }

  get remaining(): number {
    return this.pending.length;
  }

  /** Materializes one node. Returns true while work remains. */
  stepOne(): boolean {
    const item = this.pending.shift();
    if (!item) return false;
    const { spec, parent } = item;
    const hostNode = this.host.createDetached(spec.type, { label: spec.label });
    const sub = new Subscription(
      `v${this.version}:${spec.type}(${spec.label ?? ""})`,
      this.tracker,
    );
    const node: RenderedNode = { spec, hostNode, sub, children: [] };
    if (parent) {
      parent.children.push(node);
      // Attaching into a detached subtree is never a visible mutation.
      this.host.attach(parent.hostNode, hostNode);
    } else {
      this.root = node;
    }
    this.nodes.push(node);
    for (const child of spec.children ?? []) {
      this.pending.push({ spec: child, parent: node });
    }
    return this.pending.length > 0;
  }

  abort(): void {
    if (this.aborted) return;
    this.aborted = true;
    for (const node of this.nodes) node.sub.dispose();
    this.nodes.length = 0;
    this.root = null;
  }
}

/**
 * Two-phase renderer.
 * - Prepare is incremental, pausable, and superseded by newer updates.
 * - Only the latest *complete* version may commit.
 * - Commit is the single atomic swap that touches the visible tree.
 * - Unmount cleans up exactly once.
 */
export class Renderer {
  private readonly host: VirtualHost;
  private readonly tracker: ResourceTracker;
  private latest: { version: number; spec: TreeSpec } | null = null;
  private session: PrepareSession | null = null;
  private committed: { version: number; root: RenderedNode } | null = null;
  private unmounted = false;

  constructor(host: VirtualHost, tracker: ResourceTracker) {
    this.host = host;
    this.tracker = tracker;
  }

  get committedVersion(): number {
    return this.committed?.version ?? 0;
  }

  get preparingVersion(): number | null {
    return this.session?.version ?? null;
  }

  schedule(version: number, spec: TreeSpec): void {
    if (this.unmounted) throw new Error("renderer is unmounted");
    // Stale updates never even start preparing.
    if (version <= this.committedVersion) return;
    if (this.latest && version <= this.latest.version) return;
    this.latest = { version, spec };
    if (this.session && this.session.version !== version) {
      // Superseded: clean up the stale prepare's subscriptions now.
      this.session.abort();
      this.session = null;
    }
  }

  /** Advances exactly one unit of work. Returns false when idle. */
  step(): boolean {
    if (this.unmounted) return false;
    if (!this.session) {
      if (!this.latest || this.latest.version <= this.committedVersion) {
        this.latest = null;
        return false;
      }
      this.session = new PrepareSession(
        this.latest.version,
        this.latest.spec,
        this.tracker,
        this.host,
      );
    }
    const hasMore = this.session.stepOne();
    if (!hasMore) {
      const done = this.session;
      this.session = null;
      // Guard: only the latest complete version may commit.
      if (this.latest && done.version === this.latest.version) {
        this.commit(done);
      } else {
        done.abort();
      }
    }
    return true;
  }

  private commit(session: PrepareSession): void {
    const root = session.root;
    if (!root) throw new Error("cannot commit an empty session");
    this.host.attach(this.host.root, root.hostNode);
    if (this.committed) {
      const old = this.committed.root;
      this.host.detach(old.hostNode);
      disposeTree(old);
    }
    this.committed = { version: session.version, root };
  }

  unmount(): void {
    if (this.unmounted) return; // cleanup runs exactly once
    this.unmounted = true;
    if (this.session) {
      this.session.abort();
      this.session = null;
    }
    if (this.committed) {
      this.host.detach(this.committed.root.hostNode);
      disposeTree(this.committed.root);
      this.committed = null;
    }
    this.latest = null;
  }
}
