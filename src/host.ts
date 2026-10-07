export interface HostNode {
  readonly id: number;
  readonly type: string;
  props: Record<string, unknown>;
  children: HostNode[];
  parent: HostNode | null;
}

/**
 * Virtual host: a stand-in for a real DOM/native host.
 * Counts mutations that touch the *visible* tree separately,
 * so tests can prove prepare-phase work never leaks into view.
 */
export class VirtualHost {
  readonly root: HostNode;
  private nextId = 1;
  /** Mutations applied to the currently visible (committed) tree. */
  visibleMutations = 0;
  totalDetach = 0;

  constructor() {
    this.root = { id: 0, type: "#root", props: {}, children: [], parent: null };
  }

  private isVisible(node: HostNode): boolean {
    let cur: HostNode | null = node;
    while (cur) {
      if (cur === this.root) return true;
      cur = cur.parent;
    }
    return false;
  }

  /** Creates a node outside any tree; never a visible mutation. */
  createDetached(type: string, props: Record<string, unknown>): HostNode {
    return { id: this.nextId++, type, props: { ...props }, children: [], parent: null };
  }

  attach(parent: HostNode, child: HostNode): void {
    if (child.parent) throw new Error(`node ${child.id} already attached`);
    child.parent = parent;
    parent.children.push(child);
    if (this.isVisible(parent)) this.visibleMutations++;
  }

  detach(node: HostNode): void {
    const parent = node.parent;
    if (!parent) throw new Error(`node ${node.id} not attached`);
    if (this.isVisible(parent)) this.visibleMutations++;
    parent.children.splice(parent.children.indexOf(node), 1);
    node.parent = null;
    this.totalDetach++;
  }

  snapshot(node: HostNode = this.root): string {
    const label = node.props.label ?? "";
    const kids = node.children.map((c) => this.snapshot(c)).join(",");
    return `${node.type}(${label})[${kids}]`;
  }
}
