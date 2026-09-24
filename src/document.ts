/**
 * Rich text document with three kinds of attribute ranges:
 *
 * - bold   : mergeable inline mark. Overlapping or adjacent bold ranges
 *            (same attributes) are merged into one.
 * - link   : ranges may conflict; a newly added link overrides the
 *            overlapping part of existing links, keeping their remnants.
 * - entity : non-overlapping and indivisible. Adding an overlapping entity
 *            throws; an edit touching the entity interior invalidates the
 *            whole entity and reports a reason.
 *
 * All public offsets are UTF-16 code unit offsets (plain JS string
 * indices). Any offset that would split a surrogate pair is snapped
 * outward to the nearest code point boundary, so a pair is never cut.
 * Line endings are just text: LF is 1 code unit, CRLF is 2.
 */

export interface BoldMark {
  type: 'bold';
  start: number;
  end: number;
}

export interface LinkMark {
  type: 'link';
  start: number;
  end: number;
  href: string;
}

export interface EntityMark {
  type: 'entity';
  start: number;
  end: number;
  kind: string;
  data?: unknown;
}

export type Mark = BoldMark | LinkMark | EntityMark;

export type InvalidationReason = 'entity-interior-edited' | 'entity-deleted';

export interface Invalidation {
  entity: EntityMark;
  reason: InvalidationReason;
  detail: string;
}

export interface EditResult {
  invalidations: Invalidation[];
}

export interface SerializedDocument {
  text: string;
  marks: Mark[];
}

const TYPE_RANK: Record<Mark['type'], number> = { entity: 0, link: 1, bold: 2 };

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

function compareMarks(a: Mark, b: Mark): number {
  if (a.start !== b.start) return a.start - b.start;
  if (a.end !== b.end) return a.end - b.end;
  if (a.type !== b.type) return TYPE_RANK[a.type] - TYPE_RANK[b.type];
  if (a.type === 'link' && b.type === 'link') {
    return a.href < b.href ? -1 : a.href > b.href ? 1 : 0;
  }
  if (a.type === 'entity' && b.type === 'entity') {
    return a.kind < b.kind ? -1 : a.kind > b.kind ? 1 : 0;
  }
  return 0;
}

function cloneMark(mark: Mark): Mark {
  return { ...mark };
}

function shiftMark(mark: Mark, delta: number): Mark {
  return { ...mark, start: mark.start + delta, end: mark.end + delta };
}

/** JSON.stringify with recursively sorted object keys (canonical output). */
function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'null';
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, v]) => v !== undefined)
    .sort(([ka], [kb]) => (ka < kb ? -1 : ka > kb ? 1 : 0))
    .map(([k, v]) => `${JSON.stringify(k)}:${stableStringify(v)}`);
  return `{${entries.join(',')}}`;
}

export class RichTextDocument {
  #text: string;
  #marks: Mark[] = [];

  constructor(text = '') {
    this.#text = text;
  }

  get text(): string {
    return this.#text;
  }

  /** Text length in UTF-16 code units. */
  get length(): number {
    return this.#text.length;
  }

  /** Normalized (sorted, merged) marks. Returns copies. */
  get marks(): Mark[] {
    this.#normalize();
    return this.#marks.map(cloneMark);
  }

  // ---------------------------------------------------------------- offsets

  #clamp(offset: number): number {
    if (!Number.isFinite(offset)) throw new TypeError(`invalid offset: ${offset}`);
    return Math.max(0, Math.min(this.#text.length, Math.trunc(offset)));
  }

  #splitsPair(offset: number): boolean {
    return (
      offset > 0 &&
      offset < this.#text.length &&
      isHighSurrogate(this.#text.charCodeAt(offset - 1)) &&
      isLowSurrogate(this.#text.charCodeAt(offset))
    );
  }

  /** Snap a point offset so it never splits a surrogate pair (rounds down). */
  snapPoint(offset: number): number {
    const o = this.#clamp(offset);
    return this.#splitsPair(o) ? o - 1 : o;
  }

  /** Snap a range start outward (rounds down) so no surrogate pair is cut. */
  snapRangeStart(offset: number): number {
    return this.snapPoint(offset);
  }

  /** Snap a range end outward (rounds up) so no surrogate pair is cut. */
  snapRangeEnd(offset: number): number {
    const o = this.#clamp(offset);
    return this.#splitsPair(o) ? o + 1 : o;
  }

  #snapRange(start: number, end: number): [number, number] {
    const lo = Math.min(start, end);
    const hi = Math.max(start, end);
    return [this.snapRangeStart(lo), this.snapRangeEnd(hi)];
  }

  // ----------------------------------------------------------------- edits

  /** Insert text at a UTF-16 offset (snapped to a code point boundary). */
  insert(offset: number, text: string): EditResult {
    const at = this.snapPoint(offset);
    return this.#replace(at, at, text);
  }

  /** Remove the text in [start, end); bounds snap outward around pairs. */
  remove(start: number, end: number): EditResult {
    const [s, e] = this.#snapRange(start, end);
    return this.#replace(s, e, '');
  }

  /** Replace [start, end) with text; bounds snap outward around pairs. */
  replace(start: number, end: number, text: string): EditResult {
    const [s, e] = this.#snapRange(start, end);
    return this.#replace(s, e, text);
  }

  #replace(start: number, end: number, inserted: string): EditResult {
    const removed = end - start;
    if (removed === 0 && inserted.length === 0) return { invalidations: [] };
    const delta = inserted.length - removed;
    const isInsertion = removed === 0;
    const invalidations: Invalidation[] = [];
    const next: Mark[] = [];

    for (const mark of this.#marks) {
      if (mark.type === 'entity') {
        this.#mapEntity(mark, start, end, delta, isInsertion, inserted, next, invalidations);
        continue;
      }
      // Splittable marks (bold, link).
      if (isInsertion) {
        if (start <= mark.start) next.push(shiftMark(mark, delta));
        else if (start >= mark.end) next.push(mark);
        else next.push({ ...mark, end: mark.end + delta }); // strictly inside: expand
        continue;
      }
      if (mark.end <= start) {
        next.push(mark);
      } else if (mark.start >= end) {
        next.push(shiftMark(mark, delta));
      } else if (mark.start < start && mark.end > end) {
        // Mark strictly contains the edit: it stretches over the new text.
        next.push({ ...mark, end: mark.end + delta });
      } else {
        // Partial overlap: keep the remnants outside the edited region.
        if (mark.start < start) next.push({ ...mark, end: start });
        if (mark.end > end) next.push({ ...mark, start: end + delta, end: mark.end + delta });
      }
    }

    this.#text = this.#text.slice(0, start) + inserted + this.#text.slice(end);
    this.#marks = next;
    this.#normalize();
    return { invalidations };
  }

  #mapEntity(
    mark: EntityMark,
    start: number,
    end: number,
    delta: number,
    isInsertion: boolean,
    inserted: string,
    next: Mark[],
    invalidations: Invalidation[],
  ): void {
    const entity = cloneMark(mark) as EntityMark;
    if (isInsertion) {
      if (start <= mark.start) {
        next.push(shiftMark(mark, delta));
      } else if (start >= mark.end) {
        next.push(mark);
      } else {
        invalidations.push({
          entity,
          reason: 'entity-interior-edited',
          detail: `insert of ${inserted.length} char(s) at offset ${start} falls inside entity [${mark.start}, ${mark.end})`,
        });
      }
      return;
    }
    if (end <= mark.start) {
      next.push(shiftMark(mark, delta));
    } else if (start >= mark.end) {
      next.push(mark);
    } else if (start <= mark.start && end >= mark.end) {
      invalidations.push({
        entity,
        reason: 'entity-deleted',
        detail: `edit [${start}, ${end}) removes the whole entity [${mark.start}, ${mark.end})`,
      });
    } else {
      invalidations.push({
        entity,
        reason: 'entity-interior-edited',
        detail: `edit [${start}, ${end}) intersects entity interior (${mark.start}, ${mark.end})`,
      });
    }
  }

  // ----------------------------------------------------------------- marks

  /** Add a bold range; it merges with overlapping/adjacent bold ranges. */
  addBold(start: number, end: number): void {
    const [s, e] = this.#snapRange(start, end);
    if (s >= e) return;
    this.#marks.push({ type: 'bold', start: s, end: e });
    this.#normalize();
  }

  /**
   * Add a link. Existing links overlapping the new range are clipped:
   * the new link owns [start, end), old links keep their remnants.
   */
  addLink(start: number, end: number, href: string): void {
    const [s, e] = this.#snapRange(start, end);
    if (s >= e) throw new RangeError('link range is empty');
    const kept: Mark[] = [];
    for (const mark of this.#marks) {
      if (mark.type !== 'link' || mark.end <= s || mark.start >= e) {
        kept.push(mark);
        continue;
      }
      if (mark.start < s) kept.push({ ...mark, end: s });
      if (mark.end > e) kept.push({ ...mark, start: e });
    }
    kept.push({ type: 'link', start: s, end: e, href });
    this.#marks = kept;
    this.#normalize();
  }

  /** Add an indivisible entity. Throws if it overlaps an existing entity. */
  addEntity(start: number, end: number, kind: string, data?: unknown): void {
    const [s, e] = this.#snapRange(start, end);
    if (s >= e) throw new RangeError('entity range is empty');
    for (const mark of this.#marks) {
      if (mark.type === 'entity' && mark.start < e && mark.end > s) {
        throw new Error(
          `entity [${s}, ${e}) overlaps existing entity [${mark.start}, ${mark.end})`,
        );
      }
    }
    const mark: EntityMark = { type: 'entity', start: s, end: e, kind };
    if (data !== undefined) mark.data = data;
    this.#marks.push(mark);
    this.#normalize();
  }

  // ---------------------------------------------------------- serialization

  /** Canonical form: marks sorted by (start, end, type, payload), bolds merged. */
  serialize(): SerializedDocument {
    this.#normalize();
    return { text: this.#text, marks: this.#marks.map(cloneMark) };
  }

  /** Canonical JSON string; identical document states produce identical strings. */
  toCanonicalJSON(): string {
    return stableStringify(this.serialize());
  }

  /** Rebuild a document from its serialized form (order-independent). */
  static deserialize(doc: SerializedDocument): RichTextDocument {
    const out = new RichTextDocument(doc.text);
    const marks = [...doc.marks].sort(compareMarks);
    for (const mark of marks) {
      if (mark.type === 'bold') out.addBold(mark.start, mark.end);
      else if (mark.type === 'link') out.addLink(mark.start, mark.end, mark.href);
      else out.addEntity(mark.start, mark.end, mark.kind, mark.data);
    }
    return out;
  }

  // -------------------------------------------------------------- normalize

  #normalize(): void {
    const marks = this.#marks.filter((m) => m.end > m.start);
    marks.sort(compareMarks);
    const merged: Mark[] = [];
    for (const mark of marks) {
      const prev = merged[merged.length - 1];
      if (prev && prev.type === 'bold' && mark.type === 'bold' && mark.start <= prev.end) {
        prev.end = Math.max(prev.end, mark.end);
        continue;
      }
      merged.push(mark);
    }
    this.#marks = merged;
  }
}
