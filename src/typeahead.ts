import type { NodeId } from './types.ts';
import { TreeModel } from './tree.ts';

/**
 * Type-ahead search over the visible, enabled rows. The buffer is bound to
 * the tree version: any tree mutation invalidates it, so matches are never
 * computed against a stale row set. The buffer also expires after `timeout`
 * milliseconds of typing inactivity.
 */
export class TypeAhead {
  private buffer = '';
  private bufferVersion = -1;
  private lastTime = 0;

  private tree: TreeModel;
  private timeout: number;
  private now: () => number;

  constructor(tree: TreeModel, timeout = 800, now: () => number = Date.now) {
    this.tree = tree;
    this.timeout = timeout;
    this.now = now;
  }

  /** Buffer as visible to callers: empty when stale w.r.t. the tree version. */
  get currentBuffer(): string {
    return this.bufferVersion === this.tree.version ? this.buffer : '';
  }

  get version(): number {
    return this.bufferVersion;
  }

  /**
   * Append a character and return the match: the next visible, enabled node
   * whose label starts with the buffer (case-insensitive), searching from
   * just after `focusId` and wrapping around.
   */
  input(ch: string, visible: readonly NodeId[], focusId: NodeId | null): NodeId | null {
    const t = this.now();
    if (this.bufferVersion !== this.tree.version || t - this.lastTime > this.timeout) {
      this.buffer = '';
    }
    this.buffer += ch.toLowerCase();
    this.lastTime = t;
    this.bufferVersion = this.tree.version;
    const n = visible.length;
    if (n === 0) return null;
    const start = focusId === null ? -1 : visible.indexOf(focusId);
    for (let k = 1; k <= n; k++) {
      const id = visible[(start + k + n) % n];
      if (this.tree.isDisabled(id)) continue;
      if (this.tree.labelOf(id).toLowerCase().startsWith(this.buffer)) return id;
    }
    return null;
  }

  /** Escape: cancel the pending search. */
  cancel(): void {
    this.buffer = '';
    this.bufferVersion = this.tree.version;
  }
}
