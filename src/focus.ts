import type { NodeId } from './types.ts';
import { TreeModel } from './tree.ts';

/**
 * Focus is a single cursor, fully separated from the selection. It records
 * the tree version it was validated against; `isStale()` lets the controller
 * re-validate deterministically after arbitrary mutations.
 */
export class FocusModel {
  private focusId: NodeId | null = null;
  private focusVersion = -1;

  private tree: TreeModel;

  constructor(tree: TreeModel) {
    this.tree = tree;
  }

  get id(): NodeId | null {
    return this.focusId;
  }

  get version(): number {
    return this.focusVersion;
  }

  isStale(): boolean {
    return this.focusVersion !== this.tree.version;
  }

  set(id: NodeId | null, version: number): void {
    this.focusId = id;
    this.focusVersion = version;
  }

  /** Rebind to the current version without moving (used by sync()). */
  touch(version: number): void {
    this.focusVersion = version;
  }
}
