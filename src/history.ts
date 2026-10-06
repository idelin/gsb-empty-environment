import type { OutlineNode } from './tree.ts';
import { moveNodes } from './move.ts';
import type { MoveResult, MoveTarget } from './move.ts';

/**
 * Undo/redo stack over immutable tree snapshots. Because moveNodes never
 * mutates its input, keeping references is enough to restore any state.
 */
export class OutlineHistory {
  private past: OutlineNode[][] = [];
  private future: OutlineNode[][] = [];
  private current: OutlineNode[];

  constructor(initial: OutlineNode[]) {
    this.current = initial;
  }

  get tree(): OutlineNode[] {
    return this.current;
  }

  /** Run a move; on success the new tree becomes current and undoable. */
  move(selectedIds: readonly string[], target: MoveTarget): MoveResult {
    const result = moveNodes(this.current, selectedIds, target);
    if (result.ok) this.commit(result.tree);
    return result;
  }

  /** Adopt an externally produced tree as the next state. */
  commit(next: OutlineNode[]): void {
    this.past.push(this.current);
    this.current = next;
    this.future = [];
  }

  get canUndo(): boolean {
    return this.past.length > 0;
  }

  get canRedo(): boolean {
    return this.future.length > 0;
  }

  undo(): boolean {
    const prev = this.past.pop();
    if (!prev) return false;
    this.future.push(this.current);
    this.current = prev;
    return true;
  }

  redo(): boolean {
    const next = this.future.pop();
    if (!next) return false;
    this.past.push(this.current);
    this.current = next;
    return true;
  }
}
