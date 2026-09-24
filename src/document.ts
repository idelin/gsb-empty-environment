/**
 * Versioned document with per-character identity.
 *
 * Each atom is exactly one Unicode code point (1 or 2 UTF-16 code units),
 * carrying a globally unique id. Public offsets are UTF-16 code-unit offsets;
 * edits are snapped to code-point boundaries so a surrogate pair is never
 * split (deletions expand outwards, insertions snap to the pair boundary).
 * '\r', '\n' (and thus CRLF) are ordinary atoms.
 */

import { Mapping } from './mapping.ts';

export interface Atom {
  readonly id: number;
  /** A single code point (`.length` is 1 or 2 UTF-16 code units). */
  readonly text: string;
}

export interface NormalizedEdit {
  /** Snapped UTF-16 start offset (never inside a surrogate pair). */
  readonly start: number;
  /** Snapped UTF-16 end offset of the deleted span. */
  readonly end: number;
  readonly insert: string;
}

export interface EditResult {
  readonly doc: TextDocument;
  readonly mapping: Mapping;
  readonly edit: NormalizedEdit;
}

interface IdBox {
  next: number;
}

export class TextDocument {
  readonly atoms: readonly Atom[];
  private readonly ids: IdBox;
  private cachedStarts: number[] | null;

  private constructor(atoms: readonly Atom[], ids: IdBox) {
    this.atoms = atoms;
    this.ids = ids;
    this.cachedStarts = null;
  }

  static fromText(text: string): TextDocument {
    const ids: IdBox = { next: 1 };
    const atoms: Atom[] = [];
    for (const ch of text) atoms.push({ id: ids.next++, text: ch });
    return new TextDocument(atoms, ids);
  }

  /** Length in UTF-16 code units (the public offset space). */
  get length(): number {
    return this.starts()[this.atoms.length];
  }

  text(): string {
    return this.atoms.map((a) => a.text).join('');
  }

  /** UTF-16 offset at which each atom starts; `starts[atoms.length]` is the doc length. */
  starts(): number[] {
    if (this.cachedStarts === null) {
      const starts = new Array<number>(this.atoms.length + 1);
      starts[0] = 0;
      for (let i = 0; i < this.atoms.length; i++) {
        starts[i + 1] = starts[i] + this.atoms[i].text.length;
      }
      this.cachedStarts = starts;
    }
    return this.cachedStarts;
  }

  /**
   * Atom index containing `offset`; `inside` is true when the offset falls in
   * the middle of a (surrogate-pair) atom. `offset == length` yields index
   * `atoms.length` with `inside === false`.
   */
  atomIndexAt(offset: number): { index: number; inside: boolean } {
    const starts = this.starts();
    let lo = 0;
    let hi = this.atoms.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (starts[mid + 1] <= offset) lo = mid + 1;
      else hi = mid;
    }
    if (lo >= this.atoms.length) return { index: this.atoms.length, inside: false };
    return { index: lo, inside: starts[lo] < offset };
  }

  /** Snap a UTF-16 offset down to the nearest code-point boundary. */
  snapOffset(offset: number): number {
    const clamped = Math.max(0, Math.min(offset, this.length));
    const info = this.atomIndexAt(clamped);
    return info.inside ? this.starts()[info.index] : clamped;
  }

  /**
   * Apply an edit given in UTF-16 offsets. Snapped so surrogate pairs are
   * never split. Returns the new version plus the structural mapping from
   * this version to it.
   */
  applyEdit(start: number, deleteLen: number, insert: string): EditResult {
    const len = this.length;
    let s = Math.max(0, Math.min(start, len));
    let e = Math.max(s, Math.min(s + deleteLen, len));
    const starts = this.starts();

    let endAtom: number;
    if (deleteLen === 0) {
      // Pure insertion: snap the point to the pair boundary, never delete.
      const info = this.atomIndexAt(s);
      if (info.inside) s = starts[info.index];
      e = s;
      endAtom = this.atomIndexAt(e).index;
    } else {
      // Deletion/replacement: expand outwards to include whole pairs.
      const sInfo = this.atomIndexAt(s);
      if (sInfo.inside) s = starts[sInfo.index];
      const eInfo = this.atomIndexAt(e);
      endAtom = eInfo.inside ? eInfo.index + 1 : eInfo.index;
      if (eInfo.inside) e = starts[endAtom];
    }

    const startAtom = this.atomIndexAt(s).index;
    const inserted: Atom[] = [];
    for (const ch of insert) inserted.push({ id: this.ids.next++, text: ch });

    const atoms = [
      ...this.atoms.slice(0, startAtom),
      ...inserted,
      ...this.atoms.slice(endAtom),
    ];
    const doc = new TextDocument(atoms, this.ids);
    let insertedLen = 0;
    for (const a of inserted) insertedLen += a.text.length;
    const mapping = Mapping.fromEdit({ start: s, deletedLen: e - s, insertedLen });
    return { doc, mapping, edit: { start: s, end: e, insert } };
  }
}
