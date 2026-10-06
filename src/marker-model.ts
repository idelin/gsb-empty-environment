/**
 * Per-character marker model: an independent reference implementation used
 * to cross-check `src/document.ts`. The text is an array of single-character
 * tokens and every anchor sits in a gap between tokens. All transformations
 * are expressed directly as token splices and gap arithmetic.
 */

import type { Affinity, Edit, NormalizedEdit, Selection, SelectionTransformOptions } from './document.ts';
import { normalizeEdit } from './document.ts';

export interface MarkerAnchor {
  id: string;
  gap: number;
  affinity: Affinity;
}

function mapGap(gap: number, edit: NormalizedEdit, affinity: Affinity): number {
  const removed = edit.to - edit.from;
  let pos = gap;
  if (removed > 0) {
    if (pos > edit.from && pos < edit.to) {
      pos = edit.from;
    } else if (pos >= edit.to) {
      pos -= removed;
    }
  }
  const inserted = edit.text.length;
  if (inserted > 0) {
    if (pos > edit.from || (pos === edit.from && affinity === 'right')) {
      pos += inserted;
    }
  }
  return pos;
}

export class MarkerDocument {
  tokens: string[];
  anchors: MarkerAnchor[] = [];
  selection: Selection | null = null;

  constructor(text: string) {
    this.tokens = [...text];
  }

  get text(): string {
    return this.tokens.join('');
  }

  addAnchor(id: string, gap: number, affinity: Affinity): void {
    this.anchors.push({ id, gap, affinity });
  }

  setSelection(start: number, end: number): void {
    this.selection = start <= end ? { start, end } : { start: end, end: start };
  }

  /** Apply one edit as a token-level delete followed by a token-level insert. */
  applyEdit(edit: Edit | NormalizedEdit, options: SelectionTransformOptions = {}): void {
    const e = 'kind' in edit ? normalizeEdit(edit) : edit;
    const startAffinity = options.startAffinity ?? 'left';
    const endAffinity = options.endAffinity ?? 'right';

    const removed = e.to - e.from;
    if (removed > 0) {
      this.tokens.splice(e.from, removed);
      for (const anchor of this.anchors) {
        if (anchor.gap > e.from && anchor.gap < e.to) {
          anchor.gap = e.from;
        } else if (anchor.gap >= e.to) {
          anchor.gap -= removed;
        }
      }
      if (this.selection) {
        this.selection = {
          start: mapGap(this.selection.start, { from: e.from, to: e.to, text: '' }, startAffinity),
          end: mapGap(this.selection.end, { from: e.from, to: e.to, text: '' }, endAffinity),
        };
      }
    }

    const chars = [...e.text];
    if (chars.length > 0) {
      this.tokens.splice(e.from, 0, ...chars);
      for (const anchor of this.anchors) {
        if (anchor.gap > e.from || (anchor.gap === e.from && anchor.affinity === 'right')) {
          anchor.gap += chars.length;
        }
      }
      if (this.selection) {
        this.selection = {
          start: mapGap(this.selection.start, { from: e.from, to: e.from, text: e.text }, startAffinity),
          end: mapGap(this.selection.end, { from: e.from, to: e.from, text: e.text }, endAffinity),
        };
      }
    }
  }
}
