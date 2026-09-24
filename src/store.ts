/**
 * Annotation ranges that track edits.
 *
 * Each endpoint has an affinity describing how it behaves when text is
 * inserted exactly at its position or the adjacent content is deleted:
 *
 * - `inward`  — the endpoint sticks to the annotation's own content.
 *   Text inserted at the left endpoint is NOT included in the range;
 *   text inserted at the right endpoint is NOT included either.
 * - `outward` — the endpoint sticks to the surrounding text.
 *   Text inserted at the endpoint IS included in the range.
 *
 * When an edit deletes everything an annotation covers, the annotation
 * collapses to a zero-width anchor at the deletion point instead of
 * being removed, and keeps tracking subsequent edits.
 */

import { assertUtf16Boundary, utf16Length } from './utf16.ts';

export type Affinity = 'inward' | 'outward';

export interface Annotation {
  id: string;
  /** UTF-16 offset of the range start (inclusive). */
  start: number;
  /** UTF-16 offset of the range end (exclusive). */
  end: number;
  startAffinity: Affinity;
  endAffinity: Affinity;
}

export interface Edit {
  /** Operation ID; edits at the same offset apply in ascending ID order. */
  id: number;
  /** UTF-16 offset where the edit applies. */
  offset: number;
  /** Number of UTF-16 code units to delete starting at `offset`. */
  deleteCount: number;
  /** Text to insert at `offset` (defaults to the empty string). */
  insertText?: string;
}

type Gravity = 'before' | 'after';

interface Snapshot {
  text: string;
  annotations: Annotation[];
}

/**
 * Maps a single position through one edit.
 *
 * `gravity` only matters when the position sits exactly on an edit
 * boundary: `before` keeps it ahead of inserted text / deletion point,
 * `after` moves it past. Positions strictly inside a deleted region
 * collapse to the deletion point.
 */
function mapPosition(p: number, offset: number, deleteCount: number, insertLength: number, gravity: Gravity): number {
  const deleteEnd = offset + deleteCount;
  if (p < offset) return p;
  if (p > deleteEnd) return p - deleteCount + insertLength;
  if (deleteCount === 0) {
    // Pure insertion exactly at p.
    return gravity === 'after' ? offset + insertLength : offset;
  }
  if (p === offset) return offset;
  if (p === deleteEnd) return gravity === 'after' ? offset + insertLength : offset;
  return offset; // strictly inside the deleted region
}

function startGravity(affinity: Affinity): Gravity {
  // inward: the start endpoint hugs the annotation content, i.e. moves
  // past text inserted at it; outward: stays ahead of inserted text.
  return affinity === 'inward' ? 'after' : 'before';
}

function endGravity(affinity: Affinity): Gravity {
  // inward: the end endpoint hugs the annotation content, i.e. stays
  // ahead of text inserted at it; outward: moves past inserted text.
  return affinity === 'inward' ? 'before' : 'after';
}

function cloneAnnotation(a: Annotation): Annotation {
  return { ...a };
}

export class AnnotationStore {
  #text: string;
  #annotations = new Map<string, Annotation>();
  #history: Snapshot[] = [];

  constructor(text = '') {
    this.#text = text;
  }

  getText(): string {
    return this.#text;
  }

  addAnnotation(annotation: Annotation): void {
    if (this.#annotations.has(annotation.id)) {
      throw new Error(`Duplicate annotation id "${annotation.id}"`);
    }
    const { start, end } = annotation;
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || start > end) {
      throw new RangeError(`Invalid range [${start}, ${end})`);
    }
    assertUtf16Boundary(this.#text, start, `Annotation "${annotation.id}" start`);
    assertUtf16Boundary(this.#text, end, `Annotation "${annotation.id}" end`);
    this.#annotations.set(annotation.id, cloneAnnotation(annotation));
  }

  removeAnnotation(id: string): boolean {
    return this.#annotations.delete(id);
  }

  getAnnotation(id: string): Annotation | undefined {
    const found = this.#annotations.get(id);
    return found ? cloneAnnotation(found) : undefined;
  }

  getAnnotations(): Annotation[] {
    return [...this.#annotations.values()].map(cloneAnnotation);
  }

  /**
   * Applies a batch of edits. Edit offsets are expressed against the
   * document state before the batch. Edits are ordered by offset, and
   * edits at the same offset are ordered by ascending operation ID, so
   * same-position insertions concatenate in ID order. The batch is
   * transactional (a failing edit rolls everything back) and counts as
   * a single undo step.
   */
  applyEdits(edits: Edit[]): void {
    if (edits.length === 0) return;
    const ordered = [...edits].sort((a, b) => a.offset - b.offset || a.id - b.id);
    const snapshot: Snapshot = {
      text: this.#text,
      annotations: this.getAnnotations(),
    };
    try {
      let shift = 0; // total length delta of all previously applied edits
      let lastOffset = -1;
      let groupPosition = 0; // running apply position within a same-offset group
      for (const edit of ordered) {
        if (edit.offset !== lastOffset) {
          groupPosition = edit.offset + shift;
          lastOffset = edit.offset;
        }
        const insertLength = utf16Length(edit.insertText ?? '');
        this.#applyOne(edit, groupPosition);
        shift += insertLength - edit.deleteCount;
        groupPosition += insertLength;
      }
    } catch (error) {
      this.#text = snapshot.text;
      this.#annotations = new Map(snapshot.annotations.map((a) => [a.id, a]));
      throw error;
    }
    this.#history.push(snapshot);
  }

  /** Restores the document and all annotation anchors to the state before the last batch. */
  undo(): boolean {
    const snapshot = this.#history.pop();
    if (!snapshot) return false;
    this.#text = snapshot.text;
    this.#annotations = new Map(snapshot.annotations.map((a) => [a.id, a]));
    return true;
  }

  #applyOne(edit: Edit, offset: number): void {
    const insertText = edit.insertText ?? '';
    const { deleteCount } = edit;
    if (!Number.isInteger(deleteCount) || deleteCount < 0) {
      throw new RangeError(`Invalid deleteCount ${deleteCount}`);
    }
    assertUtf16Boundary(this.#text, offset, `Edit ${edit.id} offset`);
    assertUtf16Boundary(this.#text, offset + deleteCount, `Edit ${edit.id} delete end`);

    const insertLength = utf16Length(insertText);
    this.#text =
      this.#text.slice(0, offset) + insertText + this.#text.slice(offset + deleteCount);

    for (const annotation of this.#annotations.values()) {
      const start = mapPosition(
        annotation.start,
        offset,
        deleteCount,
        insertLength,
        startGravity(annotation.startAffinity),
      );
      const end = mapPosition(
        annotation.end,
        offset,
        deleteCount,
        insertLength,
        endGravity(annotation.endAffinity),
      );
      // A covering delete collapses the range to a zero-width anchor;
      // clamp defensively so the length can never go negative.
      annotation.start = start;
      annotation.end = Math.max(start, end);
    }
  }
}
