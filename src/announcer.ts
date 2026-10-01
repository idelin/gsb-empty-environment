import type { LayoutTree, NodeId } from './tree.ts';

export interface Announcement {
  seq: number;
  id: NodeId;
  reason: string;
}

/**
 * Screen-reader focus synchronization point. Every committed focus change
 * is announced here, and announcing an invalid (invisible/deleted) node is
 * a hard error — the log must always mirror a legal focus trajectory.
 */
export class FocusAnnouncer {
  readonly log: Announcement[] = [];
  private readonly tree: LayoutTree;

  constructor(tree: LayoutTree) {
    this.tree = tree;
  }

  announce(id: NodeId, reason: string): void {
    if (!this.tree.isFocusableNow(id)) {
      throw new Error(`refusing to announce focus on invalid node "${id}" (${reason})`);
    }
    this.log.push({ seq: this.log.length + 1, id, reason });
  }
}
