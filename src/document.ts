import { Affinity, Anchor, Edit } from './types';
import { applyEdits, transformPositionThroughEdits, validateEdits } from './transform';

interface Snapshot {
  text: string;
  anchors: Map<string, Anchor>;
}

function cloneAnchors(anchors: Map<string, Anchor>): Map<string, Anchor> {
  const copy = new Map<string, Anchor>();
  for (const [id, anchor] of anchors) copy.set(id, { ...anchor });
  return copy;
}

/**
 * A text document with boundary anchors and undo/redo.
 *
 * Every mutation is expressed as edits in ORIGINAL coordinates, transforms
 * all anchors through the edit(s), and records a snapshot so undo restores
 * both the text and the anchors exactly.
 */
export class AnchorDocument {
  private text: string;
  private anchors = new Map<string, Anchor>();
  private undoStack: Snapshot[] = [];
  private redoStack: Snapshot[] = [];

  constructor(initialText = '') {
    this.text = initialText;
  }

  getText(): string {
    return this.text;
  }

  get length(): number {
    return this.text.length;
  }

  /** Attach a new anchor to a boundary. */
  addAnchor(id: string, pos: number, affinity: Affinity = 'right'): Anchor {
    if (this.anchors.has(id)) throw new Error(`Duplicate anchor id: ${id}`);
    if (!Number.isInteger(pos) || pos < 0 || pos > this.text.length) {
      throw new RangeError(`Anchor position ${pos} out of bounds [0, ${this.text.length}]`);
    }
    const anchor: Anchor = { id, pos, affinity };
    this.anchors.set(id, anchor);
    return { ...anchor };
  }

  getAnchor(id: string): Anchor | undefined {
    const anchor = this.anchors.get(id);
    return anchor ? { ...anchor } : undefined;
  }

  removeAnchor(id: string): boolean {
    return this.anchors.delete(id);
  }

  listAnchors(): Anchor[] {
    return [...this.anchors.values()].map((a) => ({ ...a }));
  }

  /** Insert `text` at `start`. */
  insert(start: number, text: string): void {
    this.applyBatch([{ start, end: start, text }]);
  }

  /** Delete the range [start, end). */
  delete(start: number, end: number): void {
    this.applyBatch([{ start, end, text: '' }]);
  }

  /** Replace the range [start, end) with `text`. */
  replace(start: number, end: number, text: string): void {
    this.applyBatch([{ start, end, text }]);
  }

  /**
   * Apply a batch of edits given in ORIGINAL coordinates. The edits must
   * not overlap. All anchors are transformed through the batch; the whole
   * batch is a single undo step.
   */
  applyBatch(edits: readonly Edit[]): void {
    validateEdits(edits);
    if (edits.length === 0) return;
    this.undoStack.push({ text: this.text, anchors: cloneAnchors(this.anchors) });
    this.redoStack = [];
    this.text = applyEdits(this.text, edits);
    for (const anchor of this.anchors.values()) {
      anchor.pos = transformPositionThroughEdits(anchor.pos, edits, anchor.affinity);
    }
  }

  /** Restore the text and anchors to their state before the last mutation. */
  undo(): boolean {
    const snapshot = this.undoStack.pop();
    if (!snapshot) return false;
    this.redoStack.push({ text: this.text, anchors: cloneAnchors(this.anchors) });
    this.text = snapshot.text;
    this.anchors = snapshot.anchors;
    return true;
  }

  redo(): boolean {
    const snapshot = this.redoStack.pop();
    if (!snapshot) return false;
    this.undoStack.push({ text: this.text, anchors: cloneAnchors(this.anchors) });
    this.text = snapshot.text;
    this.anchors = snapshot.anchors;
    return true;
  }
}
