import { Affinity, Edit } from '../src/types';

/**
 * Per-character marker model used as an independent oracle.
 *
 * The document is an array of unique character markers. An anchor is NOT a
 * numeric position: it is the gap between two markers, captured as the
 * identities of its left and right neighbour markers. After an edit
 * removes/inserts markers, the anchor is re-located purely from marker
 * identity:
 *  - right affinity prefers the right neighbour (lands after text inserted
 *    at the gap), left affinity prefers the left neighbour;
 *  - if the preferred neighbour was deleted, the other one is used;
 *  - if both neighbours were deleted (the gap lay inside a deleted range),
 *    the anchor snaps to the deletion start.
 */
export class MarkerModel {
  private ids: number[] = [];
  private chars = new Map<number, string>();
  private nextId = 1;

  static from(text: string): MarkerModel {
    const model = new MarkerModel();
    for (const ch of text) model.pushChar(ch);
    return model;
  }

  private pushChar(ch: string): void {
    const id = this.nextId++;
    this.chars.set(id, ch);
    this.ids.push(id);
  }

  text(): string {
    return this.ids.map((id) => this.chars.get(id) as string).join('');
  }

  get length(): number {
    return this.ids.length;
  }

  /** Capture the gap `gap` (0..length) as marker identities. */
  capture(gap: number): GapAnchor {
    if (gap < 0 || gap > this.ids.length) throw new RangeError(`gap ${gap}`);
    return {
      leftId: gap > 0 ? this.ids[gap - 1] : null,
      rightId: gap < this.ids.length ? this.ids[gap] : null,
      gap,
    };
  }

  /**
   * Apply one edit (coordinates valid for the CURRENT model state) and
   * re-locate every captured anchor from marker identities.
   */
  applyEdit(edit: Edit, anchors: Array<{ anchor: GapAnchor; affinity: Affinity }>): void {
    // Mutate the marker array.
    const removed = edit.end - edit.start;
    this.ids.splice(edit.start, removed);
    const inserted: number[] = [];
    for (const ch of edit.text) {
      const id = this.nextId++;
      this.chars.set(id, ch);
      inserted.push(id);
    }
    this.ids.splice(edit.start, 0, ...inserted);

    // Re-locate anchors, then re-capture their new neighbour markers so
    // subsequent edits resolve against up-to-date identities.
    for (const entry of anchors) {
      const gap = this.resolve(entry.anchor, entry.affinity, edit.start);
      entry.anchor.gap = gap;
      entry.anchor.leftId = gap > 0 ? this.ids[gap - 1] : null;
      entry.anchor.rightId = gap < this.ids.length ? this.ids[gap] : null;
    }
  }

  private resolve(anchor: GapAnchor, affinity: Affinity, deletionStart: number): number {
    const { leftId, rightId } = anchor;
    const leftIndex = leftId === null ? -2 : this.ids.indexOf(leftId);
    const rightIndex = rightId === null ? -2 : this.ids.indexOf(rightId);
    // A neighbour marker deleted by this edit means the gap lay inside (or
    // at the edge of) the deleted range: snap to the deletion start.
    if (leftIndex === -1 || rightIndex === -1) {
      return Math.min(deletionStart, this.ids.length);
    }
    // Both neighbours survive (null = document edge, which always exists).
    // They differ only when text was inserted into this exact gap; the
    // affinity then decides on which side of the insertion the gap lands.
    const leftGap = leftId === null ? 0 : leftIndex + 1;
    const rightGap = rightId === null ? this.ids.length : rightIndex;
    return affinity === 'left' ? leftGap : rightGap;
  }
}

export interface GapAnchor {
  leftId: number | null;
  rightId: number | null;
  gap?: number;
}
